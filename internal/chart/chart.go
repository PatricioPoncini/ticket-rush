// Package chart renders the comparison charts as standalone SVG files, using
// only the standard library.
package chart

import (
	"fmt"
	"html"
	"math"
	"strings"

	"github.com/PatricioPoncini/ticket-rush/internal/sim"
)

// Palette: categorical slots validated for adjacent-pair colorblind
// separation on a white surface. Labels always use text ink, never the
// series color.
var colors = map[sim.Scenario]string{
	sim.NoProtection: "#257de0",
	sim.RateLimit:    "#f47230",
	sim.WaitingRoom:  "#00b894",
	sim.LoadShedding: "#f2a600",
}

const (
	surface   = "#ffffff"
	inkStrong = "#191919"
	inkSoft   = "#666666"
	grid      = "#e7e7e7"
	axis      = "#c2c2c2"
	font      = `system-ui, -apple-system, "Segoe UI", sans-serif`
)

// Labels holds every piece of text, so the charts can be rendered in
// Spanish or English.
type Labels struct {
	Scenario map[sim.Scenario]string

	GoodputTitle    string
	GoodputSubtitle string
	GoodputY        string
	SecondsX        string
	CapacityLabel   string

	FairnessTitle    string
	FairnessSubtitle string
	FairnessX        string
	FairnessY        string
	FairnessNote     string // printed inside a panel; %d is replaced by the number of buyers
}

// LabelsFor returns the text for "es" or "en".
func LabelsFor(lang string) Labels {
	if lang == "en" {
		return Labels{
			Scenario: map[sim.Scenario]string{
				sim.NoProtection: "No protection",
				sim.RateLimit:    "Rate limiting",
				sim.WaitingRoom:  "Waiting room",
				sim.LoadShedding: "Load shedding",
			},
			GoodputTitle:     "Successful purchases per second",
			GoodputSubtitle:  "20,000 buyers in 10 seconds, backend capacity 500 requests/s (simulation)",
			GoodputY:         "purchases/s",
			SecondsX:         "seconds after the sale opens",
			CapacityLabel:    "capacity",
			FairnessTitle:    "Who buys first?",
			FairnessSubtitle: "Each dot is a buyer who got a ticket: arrival order vs. when they bought",
			FairnessX:        "arrival order",
			FairnessY:        "bought at (s)",
			FairnessNote:     "only %d bought",
		}
	}
	return Labels{
		Scenario: map[sim.Scenario]string{
			sim.NoProtection: "Sin protección",
			sim.RateLimit:    "Rate limiting",
			sim.WaitingRoom:  "Sala de espera",
			sim.LoadShedding: "Load shedding",
		},
		GoodputTitle:     "Compras exitosas por segundo",
		GoodputSubtitle:  "20.000 compradores en 10 segundos, backend con capacidad de 500 requests/s (simulación)",
		GoodputY:         "compras/s",
		SecondsX:         "segundos desde que abre la venta",
		CapacityLabel:    "capacidad",
		FairnessTitle:    "¿Quién compra primero?",
		FairnessSubtitle: "Cada punto es un comprador que consiguió entrada: orden de llegada vs. cuándo compró",
		FairnessX:        "orden de llegada",
		FairnessY:        "compró en (s)",
		FairnessNote:     "solo compraron %d",
	}
}

type svg struct{ b strings.Builder }

func (s *svg) f(format string, args ...any) { fmt.Fprintf(&s.b, format, args...) }

func (s *svg) text(x, y float64, anchor, fill string, size int, weight int, txt string) {
	s.f(`<text x="%.1f" y="%.1f" text-anchor="%s" fill="%s" font-size="%d" font-weight="%d">%s</text>`+"\n",
		x, y, anchor, fill, size, weight, html.EscapeString(txt))
}

func (s *svg) open(w, h int) {
	s.f(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" font-family='%s'>`+"\n", w, h, w, h, font)
	s.f(`<rect width="%d" height="%d" rx="12" fill="%s"/>`+"\n", w, h, surface)
}

func (s *svg) legend(x, y float64, l Labels, order []sim.Scenario) {
	for _, sc := range order {
		s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="3" stroke-linecap="round"/>`+"\n", x, y-4, x+16, y-4, colors[sc])
		name := l.Scenario[sc]
		s.text(x+22, y, "start", inkSoft, 13, 400, name)
		x += 22 + float64(len([]rune(name)))*7.2 + 22
	}
}

// Goodput draws purchases per second for every scenario on one axis.
func Goodput(results []*sim.Result, l Labels, seconds int) string {
	const W, H = 880, 460
	const left, right, top, bottom = 64.0, 150.0, 104.0, 56.0
	pw, ph := W-left-right, H-top-bottom
	capacity := results[0].Summary.Capacity
	yMax := math.Ceil(capacity*1.1/100) * 100
	x := func(t float64) float64 { return left + t/float64(seconds)*pw }
	y := func(v float64) float64 { return top + ph - v/yMax*ph }

	var s svg
	s.open(W, H)
	s.text(24, 36, "start", inkStrong, 18, 600, l.GoodputTitle)
	s.text(24, 58, "start", inkSoft, 13, 400, l.GoodputSubtitle)
	order := scenariosOf(results)
	s.legend(24, 86, l, order)

	for v := 0.0; v <= yMax; v += 100 {
		s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`+"\n", left, y(v), left+pw, y(v), grid)
		s.text(left-8, y(v)+4, "end", inkSoft, 12, 400, fmt.Sprintf("%.0f", v))
	}
	s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`+"\n", left, y(0), left+pw, y(0), axis)
	for t := 0; t <= seconds; t += 10 {
		s.text(x(float64(t)), top+ph+20, "middle", inkSoft, 12, 400, fmt.Sprint(t))
	}
	s.text(left+pw/2, H-12, "middle", inkSoft, 12, 400, l.SecondsX)
	s.f(`<text transform="translate(18 %.1f) rotate(-90)" text-anchor="middle" fill="%s" font-size="12">%s</text>`+"\n", top+ph/2, inkSoft, html.EscapeString(l.GoodputY))

	// Capacity reference line.
	s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`+"\n", left, y(capacity), left+pw, y(capacity), inkSoft)
	s.text(left+pw+8, y(capacity)+4, "start", inkSoft, 12, 400, fmt.Sprintf("%s %.0f/s", l.CapacityLabel, capacity))

	type endLabel struct {
		y    float64
		x    float64
		name string
		sc   sim.Scenario
	}
	var labels []endLabel
	for _, r := range results {
		var pts []string
		lastNonZero := 0
		for t := 0; t <= seconds && t < len(r.Seconds); t++ {
			v := float64(r.Seconds[t].Purchases)
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(float64(t)+0.5), y(v)))
			if v > 0 {
				lastNonZero = t
			}
		}
		s.f(`<polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`+"\n", strings.Join(pts, " "), colors[r.Summary.Scenario])
		// Label each line at the last second it was still selling, where the
		// lines are apart from each other.
		// A line that collapses early is labeled on the floor, after the
		// drop, so its label does not sit on top of the others.
		if lastNonZero < seconds/5 {
			labels = append(labels, endLabel{y: y(0), x: x(float64(lastNonZero) + 1.5), name: l.Scenario[r.Summary.Scenario], sc: r.Summary.Scenario})
			continue
		}
		v := float64(r.Seconds[lastNonZero-1].Purchases)
		labels = append(labels, endLabel{y: y(v), x: x(float64(lastNonZero) + 0.5), name: l.Scenario[r.Summary.Scenario], sc: r.Summary.Scenario})
	}
	for _, lb := range labels {
		s.f(`<circle cx="%.1f" cy="%.1f" r="4" fill="%s" stroke="%s" stroke-width="2"/>`+"\n", lb.x, lb.y, colors[lb.sc], surface)
		s.text(lb.x+8, lb.y-8, "start", inkStrong, 12, 600, lb.name)
	}
	s.f("</svg>\n")
	return s.b.String()
}

// Fairness draws one small panel per scenario: arrival order on x, purchase
// time on y. A thin rising band means first come, first served.
func Fairness(results []*sim.Result, l Labels, seconds int) string {
	const W, H = 880, 520
	const top, gapX, gapY, padL, padB = 96.0, 40.0, 56.0, 56.0, 40.0
	cols := 2
	rows := (len(results) + 1) / 2
	cellW := (W - 24.0 - 24.0 - gapX*float64(cols-1)) / float64(cols)
	cellH := (H - top - 24.0 - gapY*float64(rows-1)) / float64(rows)

	var s svg
	s.open(W, H)
	s.text(24, 36, "start", inkStrong, 18, 600, l.FairnessTitle)
	s.text(24, 58, "start", inkSoft, 13, 400, l.FairnessSubtitle)

	for i, r := range results {
		ox := 24 + float64(i%cols)*(cellW+gapX)
		oy := top + float64(i/cols)*(cellH+gapY)
		px, py := ox+padL, oy+8
		pw, ph := cellW-padL, cellH-padB-8
		buyers := float64(r.Summary.Buyers)
		x := func(o int) float64 { return px + float64(o)/buyers*pw }
		y := func(t float64) float64 { return py + ph - t/float64(seconds)*ph }

		s.text(ox, oy, "start", inkStrong, 13, 600, l.Scenario[r.Summary.Scenario])
		for t := 0; t <= seconds; t += 20 {
			s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`+"\n", px, y(float64(t)), px+pw, y(float64(t)), grid)
			s.text(px-6, y(float64(t))+4, "end", inkSoft, 11, 400, fmt.Sprint(t))
		}
		s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`+"\n", px, py+ph, px+pw, py+ph, axis)
		s.text(px, py+ph+16, "start", inkSoft, 11, 400, "1")
		s.text(px+pw, py+ph+16, "end", inkSoft, 11, 400, fmt.Sprintf("%d", r.Summary.Buyers))
		s.text(px+pw/2, py+ph+16, "middle", inkSoft, 11, 400, l.FairnessX)
		s.f(`<text transform="translate(%.1f %.1f) rotate(-90)" text-anchor="middle" fill="%s" font-size="11">%s</text>`+"\n", ox+10, py+ph/2, inkSoft, html.EscapeString(l.FairnessY))

		step := max(1, len(r.Purchases)/1500)
		s.f(`<g fill="%s" fill-opacity="0.55">`+"\n", colors[r.Summary.Scenario])
		for j := 0; j < len(r.Purchases); j += step {
			p := r.Purchases[j]
			if p.BoughtAt > float64(seconds) {
				continue
			}
			s.f(`<circle cx="%.1f" cy="%.1f" r="1.6"/>`, x(p.ArrivalOrder), y(p.BoughtAt))
		}
		s.f("</g>\n")
		if r.Summary.Purchased < r.Summary.Buyers/5 {
			s.text(px+pw/2, py+ph/2, "middle", inkSoft, 12, 400, fmt.Sprintf(l.FairnessNote, r.Summary.Purchased))
		}
	}
	s.f("</svg>\n")
	return s.b.String()
}

func scenariosOf(results []*sim.Result) []sim.Scenario {
	out := make([]sim.Scenario, len(results))
	for i, r := range results {
		out[i] = r.Summary.Scenario
	}
	return out
}
