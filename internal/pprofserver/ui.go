package pprofserver

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	netpprof "net/http/pprof"
	"net/url"
	"strconv"
	"strings"
)

// The visualizer is two pages: an index offering every profile and its
// parameters, and a flame page rendering one profile. Profiles are read
// in-process through the same net/http/pprof handlers the raw endpoints use,
// so both views of a profile agree by construction.

type (
	// profileType describes one profile the visualizer offers.
	profileType struct {
		name   string
		title  string
		cpu    bool   // read by sampling for a duration rather than snapshotting
		prefer string // the sample value this profile's view shows
	}

	// uiHandler serves the visualizer pages.
	uiHandler struct{}
)

// profileTypes lists the profiles on offer, in display order. Each names the
// sample value its view should show.
var profileTypes = []profileType{
	{name: "cpu", title: "CPU", cpu: true, prefer: "time"},
	{name: "heap", title: "Heap (in use)", prefer: "inuse_space"},
	{name: "allocs", title: "Heap (allocated)", prefer: "alloc_space"},
	{name: "goroutine", title: "Goroutines", prefer: "goroutines"},
	{name: "block", title: "Blocking", prefer: "delay"},
	{name: "mutex", title: "Mutex", prefer: "delay"},
	{name: "threadcreate", title: "Thread creation", prefer: "threadcreate"},
}

// ServeHTTP answers the visualizer's two pages and nothing else.
func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch strings.TrimPrefix(r.URL.Path, route+"ui") {
	case "/":
		h.index(w)
	case "/flame":
		h.flame(w, r)
	default:
		http.NotFound(w, r)
	}
}

// pageCSS is the shared styling of both pages.
const pageCSS = `
body { font-family: system-ui, sans-serif; margin: 0 auto; max-width: 1200px; padding: 16px; color: #1a1a1a; }
h1 { font-size: 20px; }
form { margin: 16px 0; line-height: 2.2; }
table { border-collapse: collapse; margin-top: 24px; width: 100%; }
th, td { border: 1px solid #ddd; padding: 4px 8px; text-align: left; font-size: 13px; }
th.num, td.num { text-align: right; }
svg.flame { width: 100%; }
.note { color: #666; font-size: 13px; }
`

func pageHeader(title string) string {
	return "<!DOCTYPE html>\n<html><head><meta charset=\"utf-8\">\n<title>" +
		html.EscapeString(title) + "</title><style>" + pageCSS + "</style></head><body>\n"
}

func pageFooter() string { return "\n</body></html>\n" }

// index renders the profile picker.
func (h *uiHandler) index(w http.ResponseWriter) {
	b := &strings.Builder{}
	b.WriteString(pageHeader("Profile the proxy"))
	b.WriteString(`<form method="get" action="flame">`)
	b.WriteString(`<label>Profile <select name="type">`)
	for _, pt := range profileTypes {
		fmt.Fprintf(b, `<option value="%s">%s</option>`, pt.name, html.EscapeString(pt.title))
	}

	b.WriteString("</select></label> ")
	b.WriteString(`<label>Seconds <input type="number" name="seconds" min="1" max="60" value="10"></label> `)
	b.WriteString(`<label><input type="checkbox" name="gc" value="1"> collect garbage first (heap)</label> `)
	b.WriteString(`<button type="submit">Render</button>`)
	b.WriteString("</form>")
	fmt.Fprintf(b, `<p class="note">The raw endpoints, which any pprof tool can read, are under <a href="%s">%s</a>.</p>`,
		route, html.EscapeString(strings.TrimSuffix(route, "/")))
	b.WriteString(pageFooter())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// flame renders one profile: the SVG flame graph and the top table.
func (h *uiHandler) flame(w http.ResponseWriter, r *http.Request) {
	body, pt, err := readProfile(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	prof, err := parseProfile(body)
	if err != nil {
		http.Error(w, "reading the profile failed: "+err.Error(), http.StatusInternalServerError)

		return
	}

	tree := buildFlame(prof, pt.prefer)

	b := &strings.Builder{}
	b.WriteString(pageHeader(pt.title + " profile"))
	b.WriteString(`<p><a href="./">Back</a></p>`)
	b.WriteString(tree.svg())
	b.WriteString(`<table><tr><th>Frame</th><th class="num">Total</th><th class="num">Self</th><th class="num">Share</th></tr>`)
	for _, s := range tree.top(25) {
		share := 0.0
		if tree.total > 0 {
			share = 100 * float64(s.total) / float64(tree.total)
		}

		fmt.Fprintf(b, `<tr><td>%s</td><td class="num">%s</td><td class="num">%s</td><td class="num">%.1f%%</td></tr>`,
			html.EscapeString(s.name), tree.format(s.total), tree.format(s.self), share)
	}

	b.WriteString("</table>")
	b.WriteString(pageFooter())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// readProfile collects the named profile through the same handlers the raw
// endpoints use. Only the parameters the profile type understands are passed
// on, so a caller cannot smuggle in, say, a text debug mode the parser would
// choke on.
func readProfile(r *http.Request) ([]byte, profileType, error) {
	name := r.FormValue("type")
	pt, ok := typeByName(name)
	if !ok {
		return nil, profileType{}, fmt.Errorf("unknown profile type %q", name)
	}

	q := url.Values{}
	if pt.cpu {
		seconds := defaultCPUSeconds
		if v, err := strconv.Atoi(r.FormValue("seconds")); err == nil && v >= 1 {
			seconds = v
		}

		if seconds > maxCPUSeconds {
			seconds = maxCPUSeconds
		}

		q.Set("seconds", strconv.Itoa(seconds))
	}

	if pt.name == "heap" && r.FormValue("gc") == "1" {
		q.Set("gc", "1")
	}

	req, err := http.NewRequestWithContext(
		r.Context(), http.MethodGet, route+pt.name+"?"+q.Encode(), nil)
	if err != nil {
		return nil, profileType{}, err // unreachable with these literals
	}

	// Plain bytes, so the parser sees exactly one encoding to undo.
	req.Header.Set("Accept-Encoding", "identity")

	cw := &captureWriter{}
	if pt.cpu {
		netpprof.Profile(cw, req)
	} else {
		netpprof.Handler(pt.name).ServeHTTP(cw, req)
	}

	if cw.code >= 400 {
		return nil, profileType{}, fmt.Errorf("the %s profile handler answered %d", pt.name, cw.code)
	}

	return cw.buf.Bytes(), pt, nil
}

// typeByName returns the profile type a form value names.
func typeByName(name string) (profileType, bool) {
	for _, pt := range profileTypes {
		if pt.name == name {
			return pt, true
		}
	}

	return profileType{}, false
}

// profile read parameters: how long a CPU profile samples for, bounded so a
// form submission cannot pin the CPU for an hour.
const (
	defaultCPUSeconds = 10
	maxCPUSeconds     = 60
)

// captureWriter is the minimal http.ResponseWriter the in-process reads use to
// collect a profile body.
type captureWriter struct {
	header http.Header
	buf    bytes.Buffer
	code   int
}

func (w *captureWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}

	return w.header
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}

	return w.buf.Write(p)
}

func (w *captureWriter) WriteHeader(code int) { w.code = code }
