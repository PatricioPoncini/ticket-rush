// Package sim is a discrete-event simulation of a ticket sale: thousands of
// buyers arrive within a few seconds at a checkout backend with a fixed
// capacity, and each scenario puts a different protection in front of it.
//
// The simulation runs in virtual time and is fully deterministic for a given
// seed, so every run of the same config produces the same numbers.
package sim

import (
	"fmt"
	"time"
)

// Scenario is the protection placed in front of the checkout backend.
type Scenario string

const (
	// NoProtection sends every request straight to the backend queue.
	NoProtection Scenario = "none"
	// RateLimit applies a global and a per-user token bucket and answers 429
	// with Retry-After to whoever is over the limit.
	RateLimit Scenario = "ratelimit"
	// WaitingRoom keeps buyers in a queue and lets them into the checkout at
	// a controlled rate (new users per second) with a cap on active users.
	WaitingRoom Scenario = "waitingroom"
	// LoadShedding rejects requests with 503 when the backend is overloaded,
	// dropping low-priority (catalog) traffic before critical (checkout).
	LoadShedding Scenario = "loadshedding"
)

// Scenarios lists every scenario in the order the charts use.
var Scenarios = []Scenario{NoProtection, RateLimit, WaitingRoom, LoadShedding}

// ParseScenario validates a scenario name.
func ParseScenario(s string) (Scenario, error) {
	for _, sc := range Scenarios {
		if string(sc) == s {
			return sc, nil
		}
	}
	return "", fmt.Errorf("unknown scenario %q (valid: none, ratelimit, waitingroom, loadshedding)", s)
}

// Config holds every parameter of a run. All numbers are illustrative; they
// are not measurements of any real system.
type Config struct {
	Scenario Scenario
	Seed     int64
	Duration time.Duration // how long the simulation runs

	// Traffic.
	Buyers        int           // people who want a ticket
	ArrivalWindow time.Duration // buyers arrive uniformly within [0, ArrivalWindow)
	CatalogRPS    float64       // background catalog traffic (browsing, search), Poisson
	PatienceMin   time.Duration // each buyer gives up after a random patience in [min, max]
	PatienceMax   time.Duration

	// Backend: Workers parallel workers, each request takes ServiceTime.
	// Capacity = Workers / ServiceTime.
	Workers     int
	ServiceTime time.Duration

	// Client behaviour.
	ClientTimeout     time.Duration // the client stops waiting after this
	BackoffBase       time.Duration // exponential backoff with full jitter...
	BackoffCap        time.Duration // ...capped at this
	ImpatientFraction float64       // share of buyers that ignore Retry-After and hammer
	ImpatientRetry    time.Duration // how often impatient buyers retry

	// Rate limiting (token buckets from golang.org/x/time/rate).
	GlobalRate   float64       // tokens per second for the whole backend
	GlobalBurst  int           // bucket size
	PerUserRate  float64       // tokens per second per buyer
	PerUserBurst int           // bucket size per buyer
	RetryAfter   time.Duration // value sent in the Retry-After header

	// Waiting room. Modeled after the two thresholds Cloudflare Waiting Room
	// exposes ("new users per minute" and "total active users"); the admission
	// rate is implemented here with a token bucket.
	AdmitPerSecond float64
	MaxActiveUsers int
	QueueMethod    string // "fifo" or "random"
	AdmitInterval  time.Duration

	// Load shedding: requests are rejected with 503 when the estimated queue
	// wait is above the threshold for their priority.
	ShedCatalogAt  time.Duration
	ShedCheckoutAt time.Duration

	// TrackedBuyer is the arrival position (0-based) of the buyer the UI follows.
	TrackedBuyer int
}

// DefaultConfig returns the parameters used for the blog post charts.
func DefaultConfig(s Scenario) Config {
	return Config{
		Scenario: s,
		Seed:     42,
		Duration: 240 * time.Second,

		Buyers:        20000,
		ArrivalWindow: 10 * time.Second,
		CatalogRPS:    100,
		PatienceMin:   60 * time.Second,
		PatienceMax:   180 * time.Second,

		Workers:     10,
		ServiceTime: 20 * time.Millisecond, // 10 workers / 20ms = 500 req/s

		ClientTimeout:     3 * time.Second,
		BackoffBase:       500 * time.Millisecond,
		BackoffCap:        8 * time.Second,
		ImpatientFraction: 0.10,
		ImpatientRetry:    250 * time.Millisecond,

		GlobalRate:   500,
		GlobalBurst:  50,
		PerUserRate:  1,
		PerUserBurst: 2,
		RetryAfter:   1 * time.Second,

		AdmitPerSecond: 350,
		MaxActiveUsers: 1000,
		QueueMethod:    "fifo",
		AdmitInterval:  100 * time.Millisecond,

		ShedCatalogAt:  100 * time.Millisecond,
		ShedCheckoutAt: 1 * time.Second,

		TrackedBuyer: 10000,
	}
}

// Capacity is the number of requests per second the backend can complete.
func (c Config) Capacity() float64 {
	return float64(c.Workers) / c.ServiceTime.Seconds()
}

// Validate rejects configurations that would make the simulation meaningless
// or too expensive to run.
func (c Config) Validate() error {
	switch {
	case c.Buyers < 1 || c.Buyers > 200000:
		return fmt.Errorf("buyers must be between 1 and 200000")
	case c.Duration < time.Second || c.Duration > 30*time.Minute:
		return fmt.Errorf("duration must be between 1s and 30m")
	case c.Workers < 1 || c.ServiceTime <= 0:
		return fmt.Errorf("workers and service time must be positive")
	case c.ArrivalWindow <= 0 || c.ClientTimeout <= 0:
		return fmt.Errorf("arrival window and client timeout must be positive")
	case c.PatienceMin <= 0 || c.PatienceMax < c.PatienceMin:
		return fmt.Errorf("invalid patience range")
	case c.CatalogRPS < 0 || c.ImpatientFraction < 0 || c.ImpatientFraction > 1:
		return fmt.Errorf("invalid catalog rate or impatient fraction")
	case c.Scenario == RateLimit && (c.GlobalRate <= 0 || c.PerUserRate <= 0 || c.GlobalBurst < 1 || c.PerUserBurst < 1):
		return fmt.Errorf("rate limits must be positive")
	case c.Scenario == WaitingRoom && (c.AdmitPerSecond <= 0 || c.MaxActiveUsers < 1 || c.AdmitInterval <= 0):
		return fmt.Errorf("waiting room parameters must be positive")
	case c.Scenario == WaitingRoom && c.QueueMethod != "fifo" && c.QueueMethod != "random":
		return fmt.Errorf("queue method must be fifo or random")
	case c.Scenario == LoadShedding && (c.ShedCatalogAt <= 0 || c.ShedCheckoutAt < c.ShedCatalogAt):
		return fmt.Errorf("load shedding thresholds must be positive and checkout's must be >= catalog's")
	case c.TrackedBuyer < 0 || c.TrackedBuyer >= c.Buyers:
		return fmt.Errorf("tracked buyer must be in [0, buyers)")
	}
	if _, err := ParseScenario(string(c.Scenario)); err != nil {
		return err
	}
	return nil
}
