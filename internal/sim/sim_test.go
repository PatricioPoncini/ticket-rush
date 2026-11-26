package sim

import (
	"reflect"
	"testing"
	"time"
)

func TestSameSeedSameResult(t *testing.T) {
	for _, sc := range Scenarios {
		a, err := Run(DefaultConfig(sc))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := Run(DefaultConfig(sc))
		if !reflect.DeepEqual(a.Seconds, b.Seconds) || a.Summary != b.Summary {
			t.Errorf("%s: two runs with the same seed differ", sc)
		}
	}
}

func TestNoProtectionCollapses(t *testing.T) {
	r, _ := Run(DefaultConfig(NoProtection))
	if got := r.Summary.Purchased; got > r.Summary.Buyers/5 {
		t.Errorf("expected goodput collapse, but %d of %d buyers bought", got, r.Summary.Buyers)
	}
	if r.Summary.Wasted == 0 {
		t.Error("expected wasted work (answers nobody waited for)")
	}
}

func TestProtectedScenariosServeEveryone(t *testing.T) {
	for _, sc := range []Scenario{RateLimit, WaitingRoom, LoadShedding} {
		r, _ := Run(DefaultConfig(sc))
		if r.Summary.Purchased != r.Summary.Buyers {
			t.Errorf("%s: %d of %d bought", sc, r.Summary.Purchased, r.Summary.Buyers)
		}
		if r.Summary.Wasted != 0 {
			t.Errorf("%s: %d wasted requests, the backend should never be past the client timeout", sc, r.Summary.Wasted)
		}
	}
}

// The admission bucket must never let more than rate*window + burst buyers
// out of the room in any window.
func TestWaitingRoomAdmissionRespectsRate(t *testing.T) {
	cfg := DefaultConfig(WaitingRoom)
	e, err := newEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.run()

	var admitted []time.Duration
	for _, b := range e.buyers {
		if b.state == stateBought || (b.state == stateGaveUp && b.admittedAt > 0) {
			admitted = append(admitted, b.admittedAt)
		}
	}
	sortDurations(admitted)
	burst := float64(e.admit.Burst())
	window := time.Second
	j := 0
	for i := range admitted {
		for admitted[i]-admitted[j] >= window {
			j++
		}
		if n := float64(i - j + 1); n > cfg.AdmitPerSecond*window.Seconds()+burst {
			t.Fatalf("%v admitted in one second at %v, limit is %v + burst %v", n, admitted[i], cfg.AdmitPerSecond, burst)
		}
	}
}

func TestWaitingRoomFIFOIsFair(t *testing.T) {
	r, _ := Run(DefaultConfig(WaitingRoom))
	if r.Summary.Fairness < 0.99 {
		t.Errorf("FIFO room should keep arrival order, fairness = %.3f", r.Summary.Fairness)
	}
	cfg := DefaultConfig(WaitingRoom)
	cfg.QueueMethod = "random"
	r, _ = Run(cfg)
	if r.Summary.Fairness > 0.5 {
		t.Errorf("random room should not keep arrival order, fairness = %.3f", r.Summary.Fairness)
	}
}

func TestLoadSheddingDropsCatalogBeforeCheckout(t *testing.T) {
	e, err := newEngine(DefaultConfig(LoadShedding))
	if err != nil {
		t.Fatal(err)
	}
	e.run()
	if e.firstCatalogShed < 0 {
		t.Fatal("catalog was never shed; the test config should overload the backend")
	}
	if e.firstCheckoutShed >= 0 && e.firstCheckoutShed < e.firstCatalogShed {
		t.Errorf("checkout shed at %v before catalog at %v", e.firstCheckoutShed, e.firstCatalogShed)
	}
}

func TestValidate(t *testing.T) {
	cfg := DefaultConfig(LoadShedding)
	cfg.ShedCheckoutAt = cfg.ShedCatalogAt / 2
	if cfg.Validate() == nil {
		t.Error("expected error when checkout is shed earlier than catalog")
	}
	cfg = DefaultConfig(WaitingRoom)
	cfg.QueueMethod = "lifo"
	if cfg.Validate() == nil {
		t.Error("expected error for unknown queue method")
	}
}
