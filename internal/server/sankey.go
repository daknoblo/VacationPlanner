package server

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// The budget's cost-flow (Sankey) diagram: payers on the left, categories on
// the right, and one ribbon per payer/category pair. The whole layout is
// computed here and rendered as inline SVG, so the diagram needs no client-side
// script — which also keeps it inside the strict Content-Security-Policy.
const (
	sankeyWidth    = 680 // viewBox width
	sankeyLabelW   = 150 // horizontal space reserved for the labels of one side
	sankeyNodeW    = 13  // width of a node bar
	sankeyGap      = 10  // vertical gap between the nodes of one column
	sankeyPad      = 10  // vertical padding above/below the columns
	sankeyHeaderH  = 22  // band above the columns holding the two column headers
	sankeyLabelPad = 8   // gap between a node bar and its label
	sankeyRowH     = 34  // vertical space aimed at per node
	sankeyMinFlow  = 150 // smallest total height of the stacked flow
	sankeyMaxFlow  = 430 // largest total height of the stacked flow
	sankeyMinLabel = 7   // node bars thinner than this stay unlabelled
)

// sankeyView is the fully positioned diagram handed to the template.
type sankeyView struct {
	Width  int
	Height int
	// Anchors for the two column headers, which the template labels itself.
	HeaderY      string
	LeftHeaderX  string
	RightHeaderX string
	Nodes        []sankeyNode
	Links        []sankeyLink
}

// sankeyNode is one bar plus its label. Payer bars carry a person color,
// category bars a `cat-N` class from the shared category palette.
type sankeyNode struct {
	X, Y, W, H string
	LabelX     string
	LabelY     string
	Anchor     string // SVG text-anchor: "end" for the left column, "start" for the right
	Label      string
	Amount     string
	Title      string
	Color      string // person color ("" when unknown or for category bars)
	CatClass   string // "cat-N" for category bars, "" for payer bars
	ShowLabel  bool
}

// sankeyLink is one ribbon from a payer to a category.
type sankeyLink struct {
	D     string // SVG path
	Color string // the paying person's color ("" = unassigned)
	Title string
}

// sankeySlot is a node's vertical position and height within its column.
type sankeySlot struct{ y, h float64 }

// sankeyParty is one payer column entry while the diagram is being aggregated.
type sankeyParty struct {
	id    string // person ID, "" for unassigned costs
	name  string
	color string
	total float64
}

// buildSankey aggregates the expenses into payer/category flows and lays the
// diagram out. It returns nil when there is nothing to draw.
func buildSankey(expenses []budgetExpense, cats []budgetCategory, currency, unassignedLabel, uncategorizedLabel string) *sankeyView {
	if unassignedLabel == "" {
		unassignedLabel = "?"
	}
	if len(cats) == 0 {
		return nil
	}

	parties, flows, total := sankeyFlows(expenses, unassignedLabel)
	if total <= 0 || len(parties) == 0 {
		return nil
	}

	rows := len(parties)
	if len(cats) > rows {
		rows = len(cats)
	}
	flowH := math.Min(math.Max(float64(rows*sankeyRowH), sankeyMinFlow), sankeyMaxFlow)
	scale := flowH / total
	columnsH := math.Ceil(flowH + 2*sankeyPad + float64(rows-1)*sankeyGap)
	height := columnsH + sankeyHeaderH

	leftVals := make([]float64, len(parties))
	for i, p := range parties {
		leftVals[i] = p.total
	}
	rightVals := make([]float64, len(cats))
	for j, c := range cats {
		rightVals[j] = c.Amount
	}
	left := sankeyColumn(leftVals, scale, columnsH, sankeyHeaderH)
	right := sankeyColumn(rightVals, scale, columnsH, sankeyHeaderH)

	leftX := float64(sankeyLabelW)
	rightX := float64(sankeyWidth - sankeyLabelW - sankeyNodeW)

	view := &sankeyView{
		Width:        sankeyWidth,
		Height:       int(height),
		HeaderY:      svgNum(sankeyHeaderH - 8),
		LeftHeaderX:  svgNum(leftX + sankeyNodeW),
		RightHeaderX: svgNum(rightX),
	}
	for i, p := range parties {
		view.Nodes = append(view.Nodes, sankeyNode{
			X: svgNum(leftX), Y: svgNum(left[i].y), W: svgNum(sankeyNodeW), H: svgNum(left[i].h),
			LabelX:    svgNum(leftX - sankeyLabelPad),
			LabelY:    svgNum(left[i].y + left[i].h/2),
			Anchor:    "end",
			Label:     p.name,
			Amount:    bmoney(p.total, currency),
			Title:     fmt.Sprintf("%s · %s", p.name, bmoney(p.total, currency)),
			Color:     p.color,
			ShowLabel: left[i].h >= sankeyMinLabel,
		})
	}
	for j, c := range cats {
		label := c.Name
		if label == "" {
			label = uncategorizedLabel
		}
		if c.Icon != "" {
			label = c.Icon + " " + label
		}
		view.Nodes = append(view.Nodes, sankeyNode{
			X: svgNum(rightX), Y: svgNum(right[j].y), W: svgNum(sankeyNodeW), H: svgNum(right[j].h),
			LabelX:    svgNum(rightX + sankeyNodeW + sankeyLabelPad),
			LabelY:    svgNum(right[j].y + right[j].h/2),
			Anchor:    "start",
			Label:     label,
			Amount:    bmoney(c.Amount, currency),
			Title:     fmt.Sprintf("%s · %s", label, bmoney(c.Amount, currency)),
			CatClass:  catClass(c.Name),
			ShowLabel: right[j].h >= sankeyMinLabel,
		})
	}

	// Ribbons, stacked in a stable order on both ends so they do not overlap.
	x0, x1 := leftX+sankeyNodeW, rightX
	xm := (x0 + x1) / 2
	srcOff := make([]float64, len(parties))
	dstOff := make([]float64, len(cats))
	for i, p := range parties {
		for j, c := range cats {
			amt := flows[p.id][c.Name]
			if amt <= 0 {
				continue
			}
			h := amt * scale
			y0 := left[i].y + srcOff[i]
			y1 := right[j].y + dstOff[j]
			label := c.Name
			if label == "" {
				label = uncategorizedLabel
			}
			view.Links = append(view.Links, sankeyLink{
				D:     sankeyRibbon(x0, y0, x1, y1, xm, h),
				Color: p.color,
				Title: fmt.Sprintf("%s → %s: %s", p.name, label, bmoney(amt, currency)),
			})
			srcOff[i] += h
			dstOff[j] += h
		}
	}
	return view
}

// sankeyFlows groups the expenses by payer and category. Payers are returned
// sorted by the amount they paid, with the unassigned bucket last.
func sankeyFlows(expenses []budgetExpense, unassignedLabel string) (parties []sankeyParty, flows map[string]map[string]float64, total float64) {
	flows = make(map[string]map[string]float64)
	index := make(map[string]int)
	for _, e := range expenses {
		if e.Amount <= 0 {
			continue
		}
		i, ok := index[e.PayerID]
		if !ok {
			name := e.PayerName
			if name == "" {
				name = unassignedLabel
			}
			parties = append(parties, sankeyParty{id: e.PayerID, name: name, color: e.PayerColor})
			i = len(parties) - 1
			index[e.PayerID] = i
			flows[e.PayerID] = make(map[string]float64)
		}
		parties[i].total += e.Amount
		flows[e.PayerID][e.Category] += e.Amount
		total += e.Amount
	}
	sort.SliceStable(parties, func(i, j int) bool {
		if (parties[i].id == "") != (parties[j].id == "") {
			return parties[j].id == "" // unassigned costs sort last
		}
		return parties[i].total > parties[j].total
	})
	return parties, flows, total
}

// sankeyColumn turns a column's values into slots, vertically centered inside
// the band that starts at offsetY and is availH tall.
func sankeyColumn(values []float64, scale, availH, offsetY float64) []sankeySlot {
	used := float64(len(values)-1) * sankeyGap
	for _, v := range values {
		used += v * scale
	}
	y := offsetY + (availH-used)/2
	out := make([]sankeySlot, len(values))
	for i, v := range values {
		h := v * scale
		out[i] = sankeySlot{y: y, h: h}
		y += h + sankeyGap
	}
	return out
}

// sankeyRibbon draws a ribbon of thickness h from (x0,y0) to (x1,y1) using two
// mirrored cubic curves that flatten out at both ends.
func sankeyRibbon(x0, y0, x1, y1, xm, h float64) string {
	var b strings.Builder
	b.WriteString("M" + svgNum(x0) + "," + svgNum(y0))
	b.WriteString(" C" + svgNum(xm) + "," + svgNum(y0) + " " + svgNum(xm) + "," + svgNum(y1) + " " + svgNum(x1) + "," + svgNum(y1))
	b.WriteString(" L" + svgNum(x1) + "," + svgNum(y1+h))
	b.WriteString(" C" + svgNum(xm) + "," + svgNum(y1+h) + " " + svgNum(xm) + "," + svgNum(y0+h) + " " + svgNum(x0) + "," + svgNum(y0+h))
	b.WriteString(" Z")
	return b.String()
}

// svgNum formats a coordinate with a single decimal to keep the markup small.
func svgNum(v float64) string {
	return strconv.FormatFloat(v, 'f', 1, 64)
}
