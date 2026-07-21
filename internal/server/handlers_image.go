package server

import (
	"fmt"
	"hash/fnv"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

// handleDestinationImage proxies a small destination photo (Wikipedia thumbnail)
// from our own origin, or returns a generated placeholder when none is found.
func (s *Server) handleDestinationImage(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) > 200 {
		q = string([]rune(q)[:200])
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")

	if q != "" {
		lang := i18n.FromContext(r.Context()).Code()
		if data, ctype, ok := s.destImg.Fetch(r.Context(), q, lang); ok {
			w.Header().Set("Content-Type", ctype)
			// data is a remote raster image (validated image/* type) and the global
			// X-Content-Type-Options: nosniff header prevents HTML sniffing.
			_, _ = w.Write(data) //nolint:gosec // validated raster image bytes, nosniff set
			return
		}
	}

	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = io.WriteString(w, placeholderSVG(q))
}

// placeholderSVG builds a deterministic gradient tile showing the first letter
// of the destination, used when no photo is available.
func placeholderSVG(name string) string {
	name = strings.TrimSpace(name)
	initial := "?"
	if r := []rune(name); len(r) > 0 {
		initial = strings.ToUpper(string(r[0]))
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(name)))
	hue := int(h.Sum32() % 360)
	c1 := fmt.Sprintf("hsl(%d,55%%,52%%)", hue)
	c2 := fmt.Sprintf("hsl(%d,55%%,38%%)", (hue+40)%360)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 160" preserveAspectRatio="xMidYMid slice">`+
		`<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="%s"/><stop offset="1" stop-color="%s"/></linearGradient></defs>`+
		`<rect width="400" height="160" fill="url(#g)"/>`+
		`<text x="200" y="98" font-family="system-ui,sans-serif" font-size="72" font-weight="700" fill="rgba(255,255,255,0.92)" text-anchor="middle">%s</text>`+
		`</svg>`, c1, c2, html.EscapeString(initial))
}
