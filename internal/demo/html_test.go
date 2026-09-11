package demo

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestTransformReadOnly(t *testing.T) {
	raw := []byte(`<!doctype html><html lang="en"><head>
<meta name="csrf-token" content="sensitive-token"><script src="/static/js/app.js"></script>
<script src="https://bad.example/tracker.js"></script><link rel="stylesheet" href="/static/css/app.css?v=123">
<link rel="preload" href="https://bad.example/font"><base href="/"><meta http-equiv="refresh" content="0; url=https://bad.example">
</head><body><a href="/">Home</a><a href="/vacations/trip">Trip</a><a href="/settings">Settings</a>
<a href="/vacations/trip/export?day=2026-06-08">Export</a><a href="/vacations/trip/export.pdf">PDF</a>
<a href="https://example.com/docs" ping="https://bad.example/track">External documentation</a>
<a href="//bad.example">Protocol relative</a><a href="javascript:alert(1)">Bad</a>
<form action="/settings" method="post" hx-post="/settings">
<input type="hidden" name="csrf_token" value="sensitive-token"><input name="title" value="Example">
<input type="password" name="api_key" value="sensitive-key"><textarea name="secret">sensitive-secret</textarea>
<button hx-delete="/vacations/trip" onclick="fetch('/api/save')">Delete</button></form>
<button data-tab="overview">Overview</button><select data-ideas-region-filter><option value="*">All</option></select>
<img src="/api/destination-image?q=mountain" srcset="https://bad.example/a 2x" onerror="alert(1)">
<img src="https://bad.example/remote.jpg"><iframe src="https://bad.example"></iframe>
<div hx-get="/vacations/trip/cheatsheet" hx-trigger="load">Loading</div>
<div draggable="true" data-day-counts-url="/vacations/trip/api/daycounts"></div>
</body></html>`)
	fragments := map[string][]byte{
		"/vacations/trip/cheatsheet": []byte(`<section id="cheatsheet"><h2>Useful phrases</h2><div hx-get="/nested">Loading nested</div></section>`),
		"/nested":                    []byte(`<p>Guten Tag &amp; hello</p><div data-day-route>12 km</div>`),
	}
	for _, language := range []string{"en", "de"} {
		result, err := transform(raw, fragments, "trip", language, "vacation.html")
		if err != nil {
			t.Fatal(err)
		}
		output := string(result)
		for _, forbidden := range []string{"hx-", "<form", "sensitive-", "/api/", "/vacations/", "srcset=", "onclick=", "onerror=", "draggable=", "app.js", "bad.example", "<iframe", "<base", `href=""`} {
			if strings.Contains(output, forbidden) {
				t.Errorf("%s output contains %q", language, forbidden)
			}
		}
		for _, expected := range []string{
			"Useful phrases", "Guten Tag &amp; hello", "12 km",
			`href="vacation.html"`, `href="export.html"`,
			`href="../../static/css/app.css"`, `src="../../static/demo/landscape.svg"`,
			`href="../en/vacation.html"`, `href="../de/vacation.html"`,
			`href="https://example.com/docs"`, `connect-src &#39;none&#39;`,
			locale(language).banner,
		} {
			if !strings.Contains(output, expected) {
				t.Errorf("%s output missing %q", language, expected)
			}
		}
		doc, err := html.Parse(bytes.NewReader(result))
		if err != nil {
			t.Fatal(err)
		}
		walk(doc, func(n *html.Node) {
			if n.Type != html.ElementNode {
				return
			}
			if (n.Data == "input" || n.Data == "textarea" || n.Data == "button" || n.Data == "select") && hasAttr(n, "disabled") == readonlyControl(n) {
				t.Errorf("wrong disabled state on %s: %+v", n.Data, n.Attr)
			}
		})
	}
}

func TestHydrationCycleRejected(t *testing.T) {
	_, err := transform([]byte(`<div hx-get="/loop"></div>`), map[string][]byte{
		"/loop": []byte(`<div hx-get="/loop"></div>`),
	}, "trip", "en", "index.html")
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestMissingFragmentRejected(t *testing.T) {
	_, err := transform([]byte(`<div hx-get="/missing">Loading</div>`), nil, "trip", "en", "index.html")
	if err == nil || !strings.Contains(err.Error(), "missing demo fragment") {
		t.Fatalf("expected missing-fragment error, got %v", err)
	}
}

func TestNavigation(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"/", "index.html"}, {"/vacations", "index.html"}, {"/about", "about.html"},
		{"/settings", "settings.html"}, {"/vacations/trip#budget", "vacation.html#budget"},
		{"/vacations/trip/export", "export.html"}, {"/vacations/trip/export.pdf", ""},
		{"/settings/backups", ""}, {"/api/geocode?q=secret", ""}, {"//example.org", ""},
		{"javascript:alert(1)", ""}, {"#overview", "#overview"},
	} {
		if got := navigation(test.input, "trip"); got != test.want {
			t.Errorf("navigation(%q) = %q; want %q", test.input, got, test.want)
		}
	}
}

func walk(n *html.Node, visit func(*html.Node)) {
	visit(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}
