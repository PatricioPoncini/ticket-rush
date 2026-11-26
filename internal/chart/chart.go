// Package chart renders the comparison charts as standalone SVG files, using
// only the standard library.
package chart

import (
	"fmt"
	"html"
	"math"
	"slices"
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
	FairnessNote     string // printed inside a panel; %s is replaced by the number of buyers
	Thousands        string // thousands separator
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
			FairnessNote:     "only %s bought",
			Thousands:        ",",
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
		FairnessNote:     "solo compraron %s",
		Thousands:        ".",
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

// Goodput draws purchases per second for every scenario on one axis, with
// the same look as the interactive demo.
func Goodput(results []*sim.Result, l Labels, seconds int) string {
	const W, H = 880, 440
	const left, right, top, bottom = 56.0, 24.0, 100.0, 56.0
	pw, ph := W-left-right, H-top-bottom
	capacity := results[0].Summary.Capacity
	peak := capacity
	for _, r := range results {
		for _, sec := range r.Seconds {
			peak = max(peak, float64(sec.Purchases))
		}
	}
	yStep := niceStep(peak * 1.15 / 4)
	yMax := math.Ceil(peak*1.15/yStep) * yStep
	xStep := int(niceStep(float64(seconds) / 8))
	tMax := (seconds + xStep - 1) / xStep * xStep
	x := func(t float64) float64 { return left + t/float64(tMax)*pw }
	y := func(v float64) float64 { return top + ph - v/yMax*ph }

	var s svg
	s.open(W, H)
	s.text(24, 36, "start", inkStrong, 18, 600, l.GoodputTitle)
	s.text(24, 58, "start", inkSoft, 13, 400, l.GoodputSubtitle)
	order := scenariosOf(results)
	s.legend(24, 84, l, order)

	for v := 0.0; v <= yMax; v += yStep {
		stroke := grid
		if v == 0 {
			stroke = axis
		}
		s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1"/>`+"\n", left, y(v), left+pw, y(v), stroke)
		s.text(left-8, y(v)+4, "end", inkSoft, 12, 400, fmt.Sprintf("%.0f", v))
	}
	for t := 0; t <= tMax; t += xStep {
		s.text(x(float64(t)), top+ph+20, "middle", inkSoft, 12, 400, fmt.Sprintf("%ds", t))
	}
	s.text(left+pw/2, H-10, "middle", inkSoft, 12, 400, l.SecondsX)

	// Capacity reference: dashed, labelled inside the plot.
	s.f(`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="%s" stroke-width="1" stroke-dasharray="4 4"/>`+"\n", left, y(capacity), left+pw, y(capacity), inkSoft)
	s.text(left+pw, y(capacity)-6, "end", inkSoft, 12, 400, fmt.Sprintf("%s %.0f/s", l.CapacityLabel, capacity))

	type endLabel struct {
		y, x float64
		name string
		sc   sim.Scenario
	}
	var labels []endLabel
	// Lines that end at zero are drawn first so the ones still selling stay on top.
	drawOrder := slices.Clone(results)
	slices.SortStableFunc(drawOrder, func(a, b *sim.Result) int {
		return lastPurchase(a) - lastPurchase(b)
	})
	for _, r := range drawOrder {
		pts := []string{fmt.Sprintf("%.1f,%.1f", x(0), y(0))}
		for t := 0; t < seconds && t < len(r.Seconds); t++ {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(float64(t+1)), y(float64(r.Seconds[t].Purchases))))
		}
		s.f(`<polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`+"\n", strings.Join(pts, " "), colors[r.Summary.Scenario])

		// Label each line at the last second it was selling at full pace,
		// where the lines are apart. A line that collapses early is labeled
		// on the floor, after the drop.
		last := lastPurchase(r)
		name := l.Scenario[r.Summary.Scenario]
		if last < seconds/5 {
			labels = append(labels, endLabel{y: y(0), x: x(float64(last) + 2), name: name, sc: r.Summary.Scenario})
			continue
		}
		t := plateauEnd(r)
		labels = append(labels, endLabel{y: y(float64(r.Seconds[t].Purchases)), x: x(float64(t + 1)), name: name, sc: r.Summary.Scenario})
	}
	for _, lb := range labels {
		s.f(`<circle cx="%.1f" cy="%.1f" r="4" fill="%s" stroke="%s" stroke-width="2"/>`+"\n", lb.x, lb.y, colors[lb.sc], surface)
		s.text(lb.x+8, lb.y-8, "start", inkStrong, 12, 600, lb.name)
	}
	s.f("</svg>\n")
	return s.b.String()
}

// SuggestedSeconds is the chart window the demo uses: a few seconds after
// the last purchase in any scenario.
func SuggestedSeconds(results []*sim.Result) int {
	last := 0
	for _, r := range results {
		last = max(last, lastPurchase(r))
	}
	return max(last+8, 30)
}

func lastPurchase(r *sim.Result) int {
	last := 0
	for _, s := range r.Seconds {
		if s.Purchases > 0 {
			last = s.T
		}
	}
	return last
}

// plateauEnd is the last second a scenario was still selling at, or close to,
// its peak pace: the spot just before its line drops.
func plateauEnd(r *sim.Result) int {
	peak := 0
	for _, s := range r.Seconds {
		peak = max(peak, s.Purchases)
	}
	end := 0
	for _, s := range r.Seconds {
		if float64(s.Purchases) >= 0.8*float64(peak) {
			end = s.T
		}
	}
	return end
}

// thousands formats n with the given separator: 20000 -> "20.000".
func thousands(n int, sep string) string {
	str := fmt.Sprint(n)
	for i := len(str) - 3; i > 0; i -= 3 {
		str = str[:i] + sep + str[i:]
	}
	return str
}

func niceStep(raw float64) float64 {
	p := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5, 10} {
		if m*p >= raw {
			return m * p
		}
	}
	return 10 * p
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
		s.text(px+pw, py+ph+16, "end", inkSoft, 11, 400, thousands(r.Summary.Buyers, l.Thousands))
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
			s.text(px+pw/2, py+ph/2, "middle", inkSoft, 12, 400, fmt.Sprintf(l.FairnessNote, thousands(r.Summary.Purchased, l.Thousands)))
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
