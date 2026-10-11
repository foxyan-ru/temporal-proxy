package pprofserver

import (
	"fmt"
	"hash/fnv"
	"html"
	"sort"
	"strings"
)

// The flame graph aggregates every sample by its frame stack and draws one row
// per stack level, each frame's width proportional to the value accumulated
// under it. allocs/op and B/op rule the repo's benchmarks; here the value is
// whatever the profile carries, chosen by pickValueIndex.

type valueKind int

const (
	countValue valueKind = iota
	timeValue
	byteValue
)

// kindFor maps a sample value's unit to how it renders.
func kindFor(unit string) valueKind {
	switch unit {
	case "nanoseconds", "nanosecond", "ns":
		return timeValue
	case "bytes", "byte":
		return byteValue
	default:
		return countValue
	}
}

// preferredValueTypes lists the sample types operators actually look at, in
// the order they are preferred. The first one present wins; falling back to
// the last type matches how pprof orders the interesting one last.
var preferredValueTypes = [...]string{
	"inuse_space",
	"alloc_space",
	"time",
	"contentions",
	"delay",
	"samples",
}

// pickValueIndex chooses which sample value the flame graph shows: the type
// the requested profile names as its interesting one when present, then the
// generally preferred list, then the last type, which is how pprof orders the
// interesting one when nothing is named.
func pickValueIndex(types []valueType, prefer string) int {
	for _, want := range append([]string{prefer}, preferredValueTypes[:]...) {
		for i, t := range types {
			if want != "" && t.name == want {
				return i
			}
		}
	}

	if len(types) > 0 {
		return len(types) - 1
	}

	return 0
}

type (
	// flameNode aggregates every sample that passes through one frame.
	flameNode struct {
		name     string
		self     int64
		total    int64
		children map[string]*flameNode
	}

	// flameTree is the aggregated profile: the root frame, the value's unit,
	// and the grand total.
	flameTree struct {
		root      *flameNode
		kind      valueKind
		valueName string
		total     int64
		maxDepth  int
	}
)

// buildFlame aggregates a profile into a flame tree. The value shown is
// pickValueIndex's choice, steered by the profile type the caller asked for;
// samples carrying zero of it contribute nothing.
func buildFlame(p *profileData, prefer string) *flameTree {
	idx := pickValueIndex(p.types, prefer)
	t := &flameTree{
		root: &flameNode{children: map[string]*flameNode{}},
	}
	if len(p.types) > 0 {
		t.valueName = p.types[idx].name
		t.kind = kindFor(p.types[idx].unit)
	}

	for _, s := range p.samples {
		v := int64(0)
		if idx < len(s.values) {
			v = s.values[idx]
		}

		if v == 0 {
			continue
		}

		t.total += v
		node := t.root
		node.total += v
		depth := 0
		// The stack is leaf-first, so walking it from the end builds the tree
		// outermost first, which is the flame graph's orientation.
		for i := len(s.stack) - 1; i >= 0; i-- {
			name := s.stack[i]
			if name == "" {
				name = "?"
			}

			child, ok := node.children[name]
			if !ok {
				child = &flameNode{name: name, children: map[string]*flameNode{}}
				node.children[name] = child
			}

			node = child
			node.total += v
			depth++
			if depth > t.maxDepth {
				t.maxDepth = depth
			}
		}

		node.self += v
	}

	return t
}

// frameStat is one row of the top table: a frame's name with its total and
// self values, summed across every branch it appears in.
type frameStat struct {
	name        string
	self, total int64
}

// top returns the busiest frames, sorted by total descending.
func (t *flameTree) top(n int) []frameStat {
	stats := map[string]*frameStat{}
	var walk func(node *flameNode)
	walk = func(node *flameNode) {
		s, ok := stats[node.name]
		if !ok {
			s = &frameStat{name: node.name}
			stats[node.name] = s
		}

		s.total += node.total
		s.self += node.self
		for _, child := range node.children {
			walk(child)
		}
	}
	walk(t.root)

	out := make([]frameStat, 0, len(stats))
	for _, s := range stats {
		out = append(out, *s)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].total > out[j].total })
	if len(out) > n {
		out = out[:n]
	}

	return out
}

// Drawing constants. The canvas is a fixed viewBox the browser scales, so the
// graph fills the page at any width.
const (
	svgWidth   = 1100
	rowHeight  = 18
	fontSize   = 11
	textMargin = 4
)

// svg renders the tree as an SVG flame graph. Every frame carries a title, so
// hovering names it and its value; text is dropped on frames too narrow to
// hold it.
func (t *flameTree) svg() string {
	b := &strings.Builder{}
	height := (t.maxDepth + 1) * rowHeight
	fmt.Fprintf(b,
		`<svg class="flame" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="100%%" preserveAspectRatio="xMidYMin meet">`,
		svgWidth, height)
	t.drawFrame(b, t.root, 0, 0, svgWidth)
	t.drawChildren(b, t.root, 0, 0, svgWidth)
	b.WriteString("</svg>")

	return b.String()
}

// drawChildren draws one row of frames: the node's children, largest first,
// left to right, then recurses under each.
func (t *flameTree) drawChildren(b *strings.Builder, node *flameNode, depth int, x, w float64) {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		return node.children[names[i]].total > node.children[names[j]].total
	})

	cx := x
	for _, name := range names {
		child := node.children[name]
		cw := w * float64(child.total) / float64(node.total)
		t.drawFrame(b, child, depth+1, cx, cw)
		t.drawChildren(b, child, depth+1, cx, cw)
		cx += cw
	}
}

func (t *flameTree) drawFrame(b *strings.Builder, node *flameNode, depth int, x, w float64) {
	if w < 1 {
		return
	}

	y := depth * rowHeight
	pct := 0.0
	if t.total > 0 {
		pct = 100 * float64(node.total) / float64(t.total)
	}

	fmt.Fprintf(b, "<g>")
	fmt.Fprintf(b, "<title>%s</title>",
		html.EscapeString(fmt.Sprintf("%s (%s, %.1f%%)", node.name, t.format(node.total), pct)))
	fmt.Fprintf(b, `<rect x="%.1f" y="%d" width="%.1f" height="%d" fill="%s"/>`,
		x, y, w, rowHeight-1, frameColor(node.name))
	if w >= 40 {
		fmt.Fprintf(b, `<text x="%.1f" y="%.1f" font-size="%d" fill="#111">%s</text>`,
			x+textMargin, float64(y+fontSize), fontSize, html.EscapeString(truncate(node.name, w)))
	}

	b.WriteString("</g>")
}

// frameColor picks a warm, deterministic color per frame name, so a function
// keeps its color across renders.
func frameColor(name string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))

	return fmt.Sprintf("hsl(%d,65%%,55%%)", h.Sum32()%360)
}

// truncate shortens name to roughly what fits in w pixels of font.
func truncate(name string, w float64) string {
	maxChars := int(w-2*textMargin) / (fontSize / 2)
	if maxChars < 1 {
		return ""
	}

	runes := []rune(name)
	if len(runes) <= maxChars {
		return name
	}

	return string(runes[:maxChars-1]) + "…"
}

// format renders a value in its unit: time as ns through s, bytes as B through
// GiB, and counts with a metric suffix.
func (t *flameTree) format(v int64) string {
	switch t.kind {
	case timeValue:
		return formatTime(v)
	case byteValue:
		return formatBytes(v)
	default:
		return formatCount(v)
	}
}

func formatTime(ns int64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2f s", float64(ns)/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", float64(ns)/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.2f µs", float64(ns)/1e3)
	default:
		return fmt.Sprintf("%d ns", ns)
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.2f MiB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.2f KiB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatCount(c int64) string {
	switch {
	case c >= 1e9:
		return fmt.Sprintf("%.2f G", float64(c)/1e9)
	case c >= 1e6:
		return fmt.Sprintf("%.2f M", float64(c)/1e6)
	case c >= 1e3:
		return fmt.Sprintf("%.2f k", float64(c)/1e3)
	default:
		return fmt.Sprintf("%d", c)
	}
}
