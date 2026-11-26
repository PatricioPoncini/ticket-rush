// Command sim runs every scenario headless, prints a summary table and writes
// per-second metrics (CSV) and the comparison charts (SVG).
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"text/tabwriter"

	"github.com/PatricioPoncini/ticket-rush/internal/chart"
	"github.com/PatricioPoncini/ticket-rush/internal/sim"
)

func main() {
	out := flag.String("out", "out", "output directory")
	seed := flag.Int64("seed", 42, "random seed")
	buyers := flag.Int("buyers", 20000, "number of buyers")
	queue := flag.String("queue", "fifo", "waiting room queue method: fifo or random")
	seconds := flag.Int("seconds", 0, "seconds shown in the charts (0: until a few seconds after the last purchase, like the demo)")
	flag.Parse()

	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}

	var results []*sim.Result
	for _, sc := range sim.Scenarios {
		cfg := sim.DefaultConfig(sc)
		cfg.Seed = *seed
		cfg.Buyers = *buyers
		cfg.TrackedBuyer = *buyers / 2
		cfg.QueueMethod = *queue
		r, err := sim.Run(cfg)
		if err != nil {
			log.Fatalf("%s: %v", sc, err)
		}
		results = append(results, r)
	}

	printSummary(results)
	if err := writeCSV(filepath.Join(*out, "results.csv"), results); err != nil {
		log.Fatal(err)
	}
	if *seconds <= 0 {
		*seconds = chart.SuggestedSeconds(results)
	}
	for _, lang := range []string{"es", "en"} {
		l := chart.LabelsFor(lang)
		write(filepath.Join(*out, "goodput-"+lang+".svg"), chart.Goodput(results, l, *seconds))
		write(filepath.Join(*out, "fairness-"+lang+".svg"), chart.Fairness(results, l, *seconds))
	}
	fmt.Printf("\nwrote %s/{results.csv,goodput-*.svg,fairness-*.svg}\n", *out)
}

func printSummary(results []*sim.Result) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "scenario\tbought\tgave up\twait p50 (s)\twait p95 (s)\trequests/buyer\terrors/buyer\tcatalog ok\twasted work\tfairness\t")
	for _, r := range results {
		s := r.Summary
		fmt.Fprintf(w, "%s\t%d\t%d\t%.1f\t%.1f\t%.1f\t%.1f\t%.0f%%\t%d\t%.2f\t\n",
			s.Scenario, s.Purchased, s.GaveUp, s.WaitP50, s.WaitP95,
			s.RequestsPerBuyer, s.ErrorsPerBuyer, s.CatalogSuccessRate*100, s.Wasted, s.Fairness)
	}
	w.Flush()
}

func writeCSV(path string, results []*sim.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"scenario", "t", "purchases", "checkout_requests", "catalog_ok", "catalog_failed",
		"rejected_429", "rejected_503", "timeouts", "wasted", "gave_up", "backend_queue", "room_size", "active_users"})
	for _, r := range results {
		for _, s := range r.Seconds {
			row := []int{s.T, s.Purchases, s.CheckoutRequests, s.CatalogOK, s.CatalogFailed,
				s.Rejected429, s.Rejected503, s.Timeouts, s.Wasted, s.GaveUp, s.BackendQueue, s.RoomSize, s.ActiveUsers}
			rec := []string{string(r.Summary.Scenario)}
			for _, v := range row {
				rec = append(rec, strconv.Itoa(v))
			}
			w.Write(rec)
		}
	}
	w.Flush()
	return w.Error()
}

func write(path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Fatal(err)
	}
}
