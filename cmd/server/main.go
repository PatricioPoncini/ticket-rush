// Command server serves the interactive demo: pick scenarios and parameters,
// open the sale, and watch the simulation play out second by second.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
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
	last := 0
	for i, cfg := range cfgs {
		res, err := sim.Run(cfg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		results[scenarios[i]] = res
		last = max(last, lastActiveSecond(res))
	}
	end := min(last+5, len(results[scenarios[0]].Seconds))

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

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
	summaries := make([]sim.Summary, 0, len(scenarios))
	for _, sc := range scenarios {
		summaries = append(summaries, results[sc].Summary)
	}
	sendEvent(w, "summary", summaries)
	flusher.Flush()
}

func sendEvent(w http.ResponseWriter, name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	return err
}

// lastActiveSecond is the last second in which anyone was still buying,
// waiting, or giving up; after it nothing interesting happens.
func lastActiveSecond(r *sim.Result) int {
	last := 0
	for _, s := range r.Seconds {
		if s.Purchases > 0 || s.GaveUp > 0 || s.RoomSize > 0 || s.CheckoutRequests > 0 {
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
