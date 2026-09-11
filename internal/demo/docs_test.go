package demo

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestRenderReadmeMarkdownWithoutRemoteResources(t *testing.T) {
	source := []byte("# Getting started\n\n**Strong** and *emphasis*.\n\n" +
		"[![CI](https://bad.example/badge.svg)](https://example.org/ci)\n\n" +
		"![Trip overview](https://daknoblo.github.io/VacationPlanner/screenshots/en/overview.png)\n\n" +
		"![German travel](screenshots/de/travel.png)\n\n" +
		"[Demo](https://daknoblo.github.io/VacationPlanner/demo/en/index.html)\n\n" +
		"[Go module](go.mod) · [Docs](README.md#getting-started) · [Section](#getting-started)\n\n" +
		"| Name | Value |\n| --- | --- |\n| **Budget** | 20 |\n\n" +
		"```html\n<script>shownAsCode()</script>\n```\n\n" +
		"<script>unsafe()</script>\n\n" +
		"<img src=\"https://bad.example/raw.png\" onerror=\"alert(1)\">\n\n" +
		"[Danger](javascript:alert%281%29)\n")
	content, err := renderReadme(source)
	if err != nil {
		t.Fatal(err)
	}
	output := string(content)
	for _, expected := range []string{`<h1 id="getting-started">Getting started</h1>`, "<strong>Strong</strong>", "<em>emphasis</em>",
		"<table>", `href="https://example.org/ci">CI</a>`, `src="screenshots/en/overview.png"`,
		`src="screenshots/de/travel.png"`, `href="demo/en/index.html"`,
		`href="https://github.com/daknoblo/VacationPlanner/blob/main/go.mod"`,
		`href="docs.html#getting-started"`, `href="#getting-started"`,
		"&lt;script&gt;shownAsCode()&lt;/script&gt;",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("Markdown output missing %q", expected)
		}
	}
	for _, forbidden := range []string{"bad.example", "<script>", "unsafe()", "onerror", "javascript:", "https://daknoblo.github.io/"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("Markdown output contains unsafe/remote markup %q", forbidden)
		}
	}
	root, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	walk(root, func(n *html.Node) {
		if n.Data == "img" && screenshotPath(attr(n, "src")) == "" {
			t.Errorf("README image is not a local gallery screenshot: %s", attr(n, "src"))
		}
	})
}

func TestScreenshotAllowlist(t *testing.T) {
	for _, input := range []string{"https://bad.example/overview.png", "//bad.example/overview.png",
		"screenshots/en/../../../private.png", "screenshots/en/%2e%2e/private.png",
		"screenshots/en/unknown.png", "screenshots/en/overview.png?remote=true", "/screenshots/en/overview.png"} {
		if got := screenshotPath(input); got != "" {
			t.Errorf("accepted unexpected image %q as %q", input, got)
		}
	}
}

func TestDocumentationLinks(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{projectSite, "index.html"},
		{projectSite + "demo/de/vacation.html#travel", "demo/de/vacation.html#travel"},
		{projectSite + "screenshots/en/accommodation.png", "screenshots/en/accommodation.png"},
		{"LICENSE", repositoryFiles + "LICENSE"},
		{"docs/BACKLOG.md", repositoryFiles + "docs/BACKLOG.md"},
		{"README.md#features", "docs.html#features"},
		{"javascript:alert(1)", ""},
		{"//bad.example", ""},
		{"/etc/passwd", ""},
		{"../../private", ""},
	} {
		if got := documentationLink(test.input); got != test.want {
			t.Errorf("documentationLink(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
