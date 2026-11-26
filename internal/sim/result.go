package sim

import (
	"math"
	"slices"
	"sort"
	"time"
)

// Second aggregates what happened during one second of the simulation.
type Second struct {
	T int `json:"t"`

	Purchases        int `json:"purchases"`         // checkouts answered in time
	CheckoutRequests int `json:"checkout_requests"` // checkout attempts, including rejected ones
	CatalogOK        int `json:"catalog_ok"`
	CatalogFailed    int `json:"catalog_failed"` // rejected or timed out
	Rejected429      int `json:"rejected_429"`
	Rejected503      int `json:"rejected_503"`
	Timeouts         int `json:"timeouts"`
	Wasted           int `json:"wasted"` // work finished after the client stopped waiting
	GaveUp           int `json:"gave_up"`

	BackendQueue int `json:"backend_queue"`
	RoomSize     int `json:"room_size"`
	ActiveUsers  int `json:"active_users"`

	TrackedState    string `json:"tracked_state"`
	TrackedPosition int    `json:"tracked_position"` // people ahead in the room, -1 if n/a
	TrackedRequests int    `json:"tracked_requests"`
	TrackedErrors   int    `json:"tracked_errors"`
}

// Summary is the outcome of a whole run.
type Summary struct {
	Scenario Scenario `json:"scenario"`
	Buyers   int      `json:"buyers"`
	Capacity float64  `json:"capacity"`

	Purchased   int `json:"purchased"`
	GaveUp      int `json:"gave_up"`
	StillTrying int `json:"still_trying"`

	// Seconds from arrival to purchase, for buyers who bought.
	WaitP50 float64 `json:"wait_p50"`
	WaitP95 float64 `json:"wait_p95"`
	// Seconds from 20:00 until the last purchase.
	LastPurchaseAt float64 `json:"last_purchase_at"`

	RequestsPerBuyer float64 `json:"requests_per_buyer"`
	ErrorsPerBuyer   float64 `json:"errors_per_buyer"`

	CatalogSuccessRate float64 `json:"catalog_success_rate"`
	Wasted             int     `json:"wasted"`

	// Spearman rank correlation between arrival order and purchase time,
	// among buyers who bought. 1 means strict first-come, first-served.
	Fairness float64 `json:"fairness"`
}

// PurchasePoint is one buyer who got a ticket: when they arrived (as an
// arrival position) and when they bought.
type PurchasePoint struct {
	ArrivalOrder int     `json:"arrival_order"`
	BoughtAt     float64 `json:"bought_at"`
}

// Result is everything a run produces.
type Result struct {
	Config    Config          `json:"-"`
	Seconds   []Second        `json:"seconds"`
	Summary   Summary         `json:"summary"`
	Purchases []PurchasePoint `json:"-"`
}

func (e *engine) result() *Result {
	sum := Summary{
		Scenario: e.cfg.Scenario,
		Buyers:   e.cfg.Buyers,
		Capacity: e.cfg.Capacity(),
	}
	var waits []float64
	var points []PurchasePoint
	totalReq, totalErr := 0, 0
	for _, b := range e.buyers {
		totalReq += b.requests
		totalErr += b.errors
		switch b.state {
		case stateBought:
			sum.Purchased++
			waits = append(waits, (b.boughtAt - b.arrival).Seconds())
			points = append(points, PurchasePoint{ArrivalOrder: b.id, BoughtAt: b.boughtAt.Seconds()})
			sum.LastPurchaseAt = math.Max(sum.LastPurchaseAt, b.boughtAt.Seconds())
		case stateGaveUp:
			sum.GaveUp++
		default:
			sum.StillTrying++
		}
	}
	sum.RequestsPerBuyer = float64(totalReq) / float64(len(e.buyers))
	sum.ErrorsPerBuyer = float64(totalErr) / float64(len(e.buyers))
	sort.Float64s(waits)
	sum.WaitP50 = percentile(waits, 0.50)
	sum.WaitP95 = percentile(waits, 0.95)

	catOK, catFail := 0, 0
	for _, s := range e.seconds {
		catOK += s.CatalogOK
		catFail += s.CatalogFailed
		sum.Wasted += s.Wasted
	}
	if catOK+catFail > 0 {
		sum.CatalogSuccessRate = float64(catOK) / float64(catOK+catFail)
	}
	sum.Fairness = spearman(points)

	return &Result{Config: e.cfg, Seconds: e.seconds, Summary: sum, Purchases: points}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

// spearman computes the rank correlation between arrival order and purchase
// time. Points are already sorted by arrival order (buyers are numbered by
// arrival), so the arrival rank is the index.
func spearman(points []PurchasePoint) float64 {
	n := len(points)
	if n < 2 {
		return 0
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	slices.SortStableFunc(idx, func(a, b int) int {
		switch {
		case points[a].BoughtAt < points[b].BoughtAt:
			return -1
		case points[a].BoughtAt > points[b].BoughtAt:
			return 1
		}
		return 0
	})
	timeRank := make([]float64, n)
	for r, i := range idx {
		timeRank[i] = float64(r)
	}
	var d2 float64
	for i := range n {
		d := float64(i) - timeRank[i]
		d2 += d * d
	}
	nf := float64(n)
	return 1 - 6*d2/(nf*(nf*nf-1))
}

func sortDurations(d []time.Duration) { slices.Sort(d) }
