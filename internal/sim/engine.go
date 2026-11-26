package sim

import (
	"container/heap"
	"math"
	"math/rand/v2"
	"time"

	"golang.org/x/time/rate"
)

// epoch anchors virtual time for golang.org/x/time/rate, whose limiters take
// an explicit time.Time. Using a fixed epoch keeps runs deterministic.
var epoch = time.Date(2026, 11, 27, 20, 0, 0, 0, time.UTC)

type buyerState int

const (
	stateNotArrived buyerState = iota
	stateInRoom                // waiting room only: queued, not admitted yet
	stateTrying                // sending checkout requests
	stateBought
	stateGaveUp
)

type buyer struct {
	id         int // arrival order, 0-based
	arrival    time.Duration
	giveUpAt   time.Duration
	impatient  bool
	state      buyerState
	boughtAt   time.Duration
	requests   int // checkout requests sent (including rejected ones)
	errors     int // 429, 503 and timeouts seen
	failures   int // consecutive failures, drives the backoff
	inflight   *request
	limiter    *rate.Limiter // per-user bucket (rate limit scenario)
	roomIdx    int           // position in the waiting room slice
	admittedAt time.Duration
}

type requestKind int

const (
	catalogReq requestKind = iota
	checkoutReq
)

type request struct {
	kind       requestKind
	buyer      *buyer // nil for catalog traffic
	deadline   time.Duration
	done       bool // the backend answered in time
	clientGone bool // the client stopped waiting (timeout or gave up)
}

type event struct {
	at  time.Duration
	seq uint64
	fn  func()
}

type eventQueue []event

func (q eventQueue) Len() int { return len(q) }
func (q eventQueue) Less(i, j int) bool {
	if q[i].at != q[j].at {
		return q[i].at < q[j].at
	}
	return q[i].seq < q[j].seq
}
func (q eventQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *eventQueue) Push(x any)   { *q = append(*q, x.(event)) }
func (q *eventQueue) Pop() any {
	old := *q
	e := old[len(old)-1]
	*q = old[:len(old)-1]
	return e
}

type engine struct {
	cfg    Config
	rng    *rand.Rand
	now    time.Duration
	seq    uint64
	events eventQueue

	buyers  []*buyer
	backend *backend

	// rate limit
	global *rate.Limiter

	// waiting room
	room     []*buyer
	roomHead int // FIFO head
	roomLive int // buyers in the room who have not given up
	active   int // admitted buyers who have not finished
	admit    *rate.Limiter

	seconds []Second

	// first moment each kind of traffic was shed (load shedding), -1 if never
	firstCatalogShed  time.Duration
	firstCheckoutShed time.Duration
}

// Run executes one simulation and returns its per-second metrics and summary.
func Run(cfg Config) (*Result, error) {
	e, err := newEngine(cfg)
	if err != nil {
		return nil, err
	}
	e.run()
	return e.result(), nil
}

func newEngine(cfg Config) (*engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	e := &engine{
		cfg:     cfg,
		rng:     rand.New(rand.NewPCG(uint64(cfg.Seed), 0x7469636b6574)),
		seconds: make([]Second, int(cfg.Duration/time.Second)),

		firstCatalogShed:  -1,
		firstCheckoutShed: -1,
	}
	for i := range e.seconds {
		e.seconds[i].T = i
		e.seconds[i].TrackedPosition = -1
	}
	e.backend = newBackend(e, cfg.Workers, cfg.ServiceTime)
	if cfg.Scenario == RateLimit {
		e.global = rate.NewLimiter(rate.Limit(cfg.GlobalRate), cfg.GlobalBurst)
	}
	if cfg.Scenario == WaitingRoom {
		burst := max(1, int(math.Ceil(cfg.AdmitPerSecond*cfg.AdmitInterval.Seconds())))
		e.admit = rate.NewLimiter(rate.Limit(cfg.AdmitPerSecond), burst)
	}

	e.scheduleBuyers()
	if cfg.CatalogRPS > 0 {
		e.at(e.expDelay(cfg.CatalogRPS), e.catalogArrival)
	}
	if cfg.Scenario == WaitingRoom {
		e.at(0, e.admitTick)
	}
	for s := 1; s <= len(e.seconds); s++ {
		e.at(time.Duration(s)*time.Second-1, e.sampleGauges)
	}

	return e, nil
}

func (e *engine) run() {
	for e.events.Len() > 0 {
		ev := heap.Pop(&e.events).(event)
		if ev.at >= e.cfg.Duration {
			break
		}
		e.now = ev.at
		ev.fn()
	}
}

// at schedules fn at absolute virtual time t.
func (e *engine) at(t time.Duration, fn func()) {
	e.seq++
	heap.Push(&e.events, event{at: t, seq: e.seq, fn: fn})
}

func (e *engine) after(d time.Duration, fn func()) { e.at(e.now+d, fn) }

func (e *engine) wall() time.Time { return epoch.Add(e.now) }

func (e *engine) sec() *Second {
	i := int(e.now / time.Second)
	if i >= len(e.seconds) {
		i = len(e.seconds) - 1
	}
	return &e.seconds[i]
}

func (e *engine) expDelay(perSecond float64) time.Duration {
	return time.Duration(e.rng.ExpFloat64() / perSecond * float64(time.Second))
}

func (e *engine) uniform(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(e.rng.Int64N(int64(hi-lo)))
}

func (e *engine) scheduleBuyers() {
	cfg := e.cfg
	arrivals := make([]time.Duration, cfg.Buyers)
	for i := range arrivals {
		arrivals[i] = e.uniform(0, cfg.ArrivalWindow)
	}
	sortDurations(arrivals)
	e.buyers = make([]*buyer, cfg.Buyers)
	for i, t := range arrivals {
		b := &buyer{
			id:        i,
			arrival:   t,
			giveUpAt:  t + e.uniform(cfg.PatienceMin, cfg.PatienceMax),
			impatient: e.rng.Float64() < cfg.ImpatientFraction,
			roomIdx:   -1,
		}
		if cfg.Scenario == RateLimit {
			b.limiter = rate.NewLimiter(rate.Limit(cfg.PerUserRate), cfg.PerUserBurst)
		}
		e.buyers[i] = b
		e.at(t, func() { e.buyerArrives(b) })
		e.at(b.giveUpAt, func() { e.giveUp(b) })
	}
}

func (e *engine) buyerArrives(b *buyer) {
	if e.cfg.Scenario == WaitingRoom {
		b.state = stateInRoom
		b.roomIdx = len(e.room)
		e.room = append(e.room, b)
		e.roomLive++
		return
	}
	b.state = stateTrying
	e.attempt(b)
}

// attempt sends one checkout request, through whatever protection the
// scenario puts in front of the backend.
func (e *engine) attempt(b *buyer) {
	if b.state != stateTrying {
		return
	}
	b.requests++
	e.sec().CheckoutRequests++

	switch e.cfg.Scenario {
	case RateLimit:
		// Per-user bucket first, so a single client hammering the API cannot
		// eat the global budget; then the global bucket that protects the backend.
		t := e.wall()
		if !b.limiter.AllowN(t, 1) || !e.global.AllowN(t, 1) {
			e.reject(b, 429, e.cfg.RetryAfter)
			return
		}
	case LoadShedding:
		if e.backend.estimatedWait() > e.cfg.ShedCheckoutAt {
			if e.firstCheckoutShed < 0 {
				e.firstCheckoutShed = e.now
			}
			e.reject(b, 503, 0)
			return
		}
	}
	e.send(&request{kind: checkoutReq, buyer: b})
}

func (e *engine) reject(b *buyer, status int, retryAfter time.Duration) {
	s := e.sec()
	if status == 429 {
		s.Rejected429++
	} else {
		s.Rejected503++
	}
	b.errors++
	e.retry(b, retryAfter)
}

// retry schedules the buyer's next attempt.
//   - Impatient clients ignore everything and retry quickly.
//   - Polite clients honor Retry-After when the server sends it.
//   - Otherwise: exponential backoff with full jitter, as described in the
//     AWS Builders' Library article "Timeouts, retries, and backoff with jitter".
func (e *engine) retry(b *buyer, retryAfter time.Duration) {
	b.failures++
	var d time.Duration
	switch {
	case b.impatient:
		d = e.cfg.ImpatientRetry
	case retryAfter > 0:
		d = retryAfter
	default:
		ceiling := e.cfg.BackoffBase << min(b.failures-1, 20)
		if ceiling > e.cfg.BackoffCap || ceiling <= 0 {
			ceiling = e.cfg.BackoffCap
		}
		d = e.uniform(0, ceiling)
	}
	e.after(d, func() { e.attempt(b) })
}

func (e *engine) send(r *request) {
	r.deadline = e.now + e.cfg.ClientTimeout
	if r.buyer != nil {
		r.buyer.inflight = r
	}
	e.backend.enqueue(r)
	e.at(r.deadline, func() { e.clientTimeout(r) })
}

func (e *engine) clientTimeout(r *request) {
	if r.done || r.clientGone {
		return
	}
	r.clientGone = true
	e.sec().Timeouts++
	if r.kind == catalogReq {
		e.sec().CatalogFailed++
		return
	}
	b := r.buyer
	b.inflight = nil
	b.errors++
	if b.state == stateTrying {
		e.retry(b, 0)
	}
}

// completed is called by the backend when it finishes processing r.
func (e *engine) completed(r *request) {
	if r.clientGone {
		// The backend did the work but nobody was waiting for the answer.
		e.sec().Wasted++
		return
	}
	r.done = true
	if r.kind == catalogReq {
		e.sec().CatalogOK++
		return
	}
	b := r.buyer
	b.inflight = nil
	b.state = stateBought
	b.boughtAt = e.now
	e.sec().Purchases++
	if e.cfg.Scenario == WaitingRoom {
		e.active--
	}
}

func (e *engine) giveUp(b *buyer) {
	switch b.state {
	case stateBought, stateGaveUp:
		return
	case stateInRoom:
		e.roomLive--
	case stateTrying:
		if e.cfg.Scenario == WaitingRoom {
			e.active--
		}
	}
	if b.inflight != nil {
		b.inflight.clientGone = true // closed the tab with a request in flight
		b.inflight = nil
	}
	b.state = stateGaveUp
	e.sec().GaveUp++
}

func (e *engine) catalogArrival() {
	e.after(e.expDelay(e.cfg.CatalogRPS), e.catalogArrival)
	switch e.cfg.Scenario {
	case RateLimit:
		if !e.global.AllowN(e.wall(), 1) {
			s := e.sec()
			s.Rejected429++
			s.CatalogFailed++
			return
		}
	case LoadShedding:
		if e.backend.estimatedWait() > e.cfg.ShedCatalogAt {
			if e.firstCatalogShed < 0 {
				e.firstCatalogShed = e.now
			}
			s := e.sec()
			s.Rejected503++
			s.CatalogFailed++
			return
		}
	}
	e.send(&request{kind: catalogReq})
}

// admitTick lets buyers out of the waiting room, at most AdmitPerSecond on
// average (token bucket) and never more than MaxActiveUsers at once.
func (e *engine) admitTick() {
	e.after(e.cfg.AdmitInterval, e.admitTick)
	for e.roomLive > 0 && e.active < e.cfg.MaxActiveUsers && e.admit.TokensAt(e.wall()) >= 1 {
		b := e.nextFromRoom()
		if b == nil {
			return
		}
		e.admit.AllowN(e.wall(), 1)
		e.roomLive--
		e.active++
		b.state = stateTrying
		b.admittedAt = e.now
		e.attempt(b)
	}
}

func (e *engine) nextFromRoom() *buyer {
	if e.cfg.QueueMethod == "random" {
		for len(e.room) > 0 {
			i := e.rng.IntN(len(e.room))
			b := e.room[i]
			last := len(e.room) - 1
			e.room[i] = e.room[last]
			e.room[i].roomIdx = i
			e.room = e.room[:last]
			b.roomIdx = -1
			if b.state == stateInRoom {
				return b
			}
		}
		return nil
	}
	for e.roomHead < len(e.room) {
		b := e.room[e.roomHead]
		e.roomHead++
		b.roomIdx = -1
		if b.state == stateInRoom {
			return b
		}
	}
	return nil
}

// sampleGauges records the point-in-time values at the end of each second.
func (e *engine) sampleGauges() {
	s := e.sec()
	s.BackendQueue = e.backend.queued()
	s.RoomSize = e.roomLive
	s.ActiveUsers = e.active

	b := e.buyers[e.cfg.TrackedBuyer]
	s.TrackedState = trackedState(b)
	if b.state == stateInRoom && e.cfg.QueueMethod == "fifo" {
		ahead := 0
		for i := e.roomHead; i < b.roomIdx; i++ {
			if e.room[i].state == stateInRoom {
				ahead++
			}
		}
		s.TrackedPosition = ahead
	}
	s.TrackedRequests = b.requests
	s.TrackedErrors = b.errors
}

func trackedState(b *buyer) string {
	switch b.state {
	case stateNotArrived:
		return "not_arrived"
	case stateInRoom:
		return "in_room"
	case stateTrying:
		return "trying"
	case stateBought:
		return "bought"
	default:
		return "gave_up"
	}
}
