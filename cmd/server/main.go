// Command server serves the interactive demo: pick scenarios and parameters,
// open the sale, and watch the simulation play out second by second.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PatricioPoncini/ticket-rush/internal/sim"
	"github.com/PatricioPoncini/ticket-rush/web"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web.Files))
	mux.HandleFunc("GET /api/run", handleRun)

	log.Printf("ticket-rush demo on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

type frame struct {
	T       int                         `json:"t"`
	Seconds map[sim.Scenario]sim.Second `json:"seconds"`
}

// handleRun runs the requested scenarios, then streams one frame per
// simulated second as Server-Sent Events, paced by the speed parameter, and
// finishes with the summaries.
func handleRun(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	scenarios, cfgs, speed, err := parseRun(q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	results := make(map[sim.Scenario]*sim.Result, len(cfgs))
	lastPurchase := 0
	for i, cfg := range cfgs {
		res, err := sim.Run(cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		results[scenarios[i]] = res
		lastPurchase = max(lastPurchase, lastPurchaseSecond(res))
	}
	// The chart stops a few seconds after the last purchase. Without
	// protection, buyers can keep failing for minutes after that; those
	// seconds are folded into a single "tail" event so the totals still add up.
	total := len(results[scenarios[0]].Seconds)
	end := min(max(lastPurchase+chartMargin, minChartSeconds), total)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	if err := sendEvent(w, "start", map[string]int{"seconds": end, "capacity": int(cfgs[0].Capacity())}); err != nil {
		return
	}
	flusher.Flush()

	tick := time.NewTicker(time.Second / time.Duration(speed))
	defer tick.Stop()
	for t := 0; t < end; t++ {
		f := frame{T: t, Seconds: make(map[sim.Scenario]sim.Second, len(results))}
		for sc, res := range results {
			f.Seconds[sc] = res.Seconds[t]
		}
		if err := sendEvent(w, "second", f); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}

	tail := make(map[sim.Scenario]tailInfo, len(results))
	for sc, res := range results {
		tail[sc] = summarizeTail(res.Seconds, end)
	}
	sendEvent(w, "tail", tail)

	// A sample of who bought when, for the "who buys first?" chart.
	points := make(map[sim.Scenario][][2]float64, len(results))
	for sc, res := range results {
		step := max(1, len(res.Purchases)/1500)
		pts := make([][2]float64, 0, len(res.Purchases)/step+1)
		for i := 0; i < len(res.Purchases); i += step {
			p := res.Purchases[i]
			pts = append(pts, [2]float64{float64(p.ArrivalOrder), math.Round(p.BoughtAt*100) / 100})
		}
		points[sc] = pts
	}
	sendEvent(w, "fairness", points)

	summaries := make([]sim.Summary, 0, len(scenarios))
	for _, sc := range scenarios {
		summaries = append(summaries, results[sc].Summary)
	}
	sendEvent(w, "summary", summaries)
	flusher.Flush()
}

const (
	chartMargin     = 8  // seconds shown after the last purchase
	minChartSeconds = 30 // never draw a chart shorter than this
)

// tailInfo aggregates the seconds after the chart ends.
type tailInfo struct {
	Seconds    int        `json:"seconds"`      // how many seconds were folded
	Totals     sim.Second `json:"totals"`       // counters summed, gauges and tracked buyer from the last second
	BoughtAt   int        `json:"bought_at"`    // second the tracked buyer bought, -1 if not in the tail
	LastGaveUp int        `json:"last_gave_up"` // last second in which a buyer gave up, -1 if none
}

func summarizeTail(secs []sim.Second, from int) tailInfo {
	info := tailInfo{BoughtAt: -1, LastGaveUp: -1}
	if from >= len(secs) {
		return info
	}
	info.Seconds = len(secs) - from
	for _, s := range secs[from:] {
		t := &info.Totals
		t.Purchases += s.Purchases
		t.CheckoutRequests += s.CheckoutRequests
		t.CatalogOK += s.CatalogOK
		t.CatalogFailed += s.CatalogFailed
		t.Rejected429 += s.Rejected429
		t.Rejected503 += s.Rejected503
		t.Timeouts += s.Timeouts
		t.Wasted += s.Wasted
		t.GaveUp += s.GaveUp
		if s.GaveUp > 0 {
			info.LastGaveUp = s.T
		}
		if s.TrackedState == "bought" && info.BoughtAt < 0 && t.TrackedState != "bought" {
			info.BoughtAt = s.T
		}
		t.BackendQueue, t.RoomSize, t.ActiveUsers = s.BackendQueue, s.RoomSize, s.ActiveUsers
		t.TrackedState, t.TrackedPosition = s.TrackedState, s.TrackedPosition
		t.TrackedRequests, t.TrackedErrors = s.TrackedRequests, s.TrackedErrors
	}
	info.Totals.T = len(secs) - 1
	return info
}

func sendEvent(w http.ResponseWriter, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	return err
}

// lastPurchaseSecond is the last second in which someone bought.
func lastPurchaseSecond(r *sim.Result) int {
	last := 0
	for _, s := range r.Seconds {
		if s.Purchases > 0 {
			last = s.T
		}
	}
	return last
}

func parseRun(q map[string][]string) ([]sim.Scenario, []sim.Config, int, error) {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	intParam := func(k string, def, lo, hi int) (int, error) {
		s := get(k)
		if s == "" {
			return def, nil
		}
		v, err := strconv.Atoi(s)
		if err != nil || v < lo || v > hi {
			return 0, fmt.Errorf("%s must be an integer between %d and %d", k, lo, hi)
		}
		return v, nil
	}

	names := strings.Split(get("scenarios"), ",")
	if get("scenarios") == "" {
		names = nil
		for _, sc := range sim.Scenarios {
			names = append(names, string(sc))
		}
	}
	buyers, err := intParam("buyers", 20000, 100, 100000)
	if err != nil {
		return nil, nil, 0, err
	}
	capacity, err := intParam("capacity", 500, 50, 5000)
	if err != nil {
		return nil, nil, 0, err
	}
	admit, err := intParam("admit", 350, 1, 5000)
	if err != nil {
		return nil, nil, 0, err
	}
	seed, err := intParam("seed", 42, 0, 1<<30)
	if err != nil {
		return nil, nil, 0, err
	}
	speed, err := intParam("speed", 10, 1, 100)
	if err != nil {
		return nil, nil, 0, err
	}
	impatient, err := intParam("impatient", 10, 0, 100)
	if err != nil {
		return nil, nil, 0, err
	}
	queue := get("queue")
	if queue == "" {
		queue = "fifo"
	}

	var scenarios []sim.Scenario
	var cfgs []sim.Config
	for _, n := range names {
		sc, err := sim.ParseScenario(n)
		if err != nil {
			return nil, nil, 0, err
		}
		cfg := sim.DefaultConfig(sc)
		cfg.Seed = int64(seed)
		cfg.Buyers = buyers
		cfg.TrackedBuyer = buyers / 2
		// Each worker handles 50 requests/s (20ms per request).
		cfg.Workers = max(1, capacity/50)
		cfg.GlobalRate = cfg.Capacity()
		cfg.AdmitPerSecond = float64(admit)
		cfg.QueueMethod = queue
		cfg.ImpatientFraction = float64(impatient) / 100
		if err := cfg.Validate(); err != nil {
			return nil, nil, 0, err
		}
		scenarios = append(scenarios, sc)
		cfgs = append(cfgs, cfg)
	}
	return scenarios, cfgs, speed, nil
}
