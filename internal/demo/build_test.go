package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/web"
	"golang.org/x/net/html"
)

func TestBuildBothLocales(t *testing.T) {
	out := t.TempDir()
	readme := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(readme, []byte("# Documentation\n\n**Formatted text** & text\n\n<script>unsafe()</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := Options{Out: out, Version: `<release & "demo">`, Readme: readme}
	if err := Build(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Generator != generator || !manifest.Synthetic || !manifest.ReadOnly || len(manifest.Languages) != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	for _, language := range []string{"en", "de"} {
		for _, page := range manifest.Pages {
			name := "demo/" + language + "/" + page
			content, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"hx-", `action=`, `<form`, "/api/", "/vacations/", "csrf-token", "csrf_token", "app.js", "htmx.min.js"} {
				if bytes.Contains(content, []byte(forbidden)) {
					t.Errorf("%s contains %q", name, forbidden)
				}
			}
			doc, err := html.Parse(bytes.NewReader(content))
			if err != nil {
				t.Fatal(err)
			}
			walk(doc, func(n *html.Node) {
				if n.Type != html.ElementNode {
					return
				}
				for _, key := range []string{"href", "src"} {
					raw := attr(n, key)
					if raw == "" || strings.HasPrefix(raw, "#") {
						continue
					}
					target, err := url.Parse(raw)
					if err != nil {
						t.Error(err)
						continue
					}
					if target.IsAbs() {
						if n.Data != "a" {
							t.Errorf("%s loads external resource %s", name, raw)
						}
						continue
					}
					base, _ := url.Parse("https://example.org/VacationPlanner/" + name)
					resolved := base.ResolveReference(target)
					if !strings.HasPrefix(resolved.Path, "/VacationPlanner/") {
						t.Errorf("%s link escapes Pages subpath: %s", name, raw)
						continue
					}
					local := strings.TrimPrefix(resolved.Path, "/VacationPlanner/")
					if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(local))); err != nil {
						t.Errorf("%s has broken %s %q: %v", name, key, raw, err)
					}
				}
			})
			if page == "vacation.html" {
				for _, expected := range []string{`id="cheatsheet-panel"`, `class="day-journey"`, "data-ideas-region-group", `id="map"`} {
					if !bytes.Contains(content, []byte(expected)) {
						t.Errorf("%s missing hydrated fixture %q", name, expected)
					}
				}
			}
		}
	}
	sourceCSS, err := web.Static.ReadFile("static/css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	builtCSS, err := os.ReadFile(filepath.Join(out, "static/css/app.css"))
	if err != nil || !bytes.Equal(sourceCSS, builtCSS) {
		t.Fatalf("real application CSS not copied intact: %v", err)
	}
	landing, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(landing, []byte("&lt;release &amp; &#34;demo&#34;&gt;")) || bytes.Contains(landing, []byte(options.Version)) {
		t.Fatal("version label was not escaped")
	}
	for _, language := range []string{"en", "de"} {
		if got := bytes.Count(landing, []byte(`src="screenshots/`+language+`/`)); got != 12 {
			t.Errorf("gallery contains %d screenshots for %s, want 12", got, language)
		}
	}
	docs, err := os.ReadFile(filepath.Join(out, "docs.html"))
	if err != nil || !bytes.Contains(docs, []byte("<strong>Formatted text</strong> &amp; text")) || bytes.Contains(docs, []byte("<script>")) {
		t.Fatalf("README was not safely rendered: %v", err)
	}
	originals := make(map[string][]byte)
	for _, name := range manifest.Files {
		content, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		originals[name] = content
	}
	// Rebuilding only overwrites known outputs; parent-provided gallery files survive.
	extra := filepath.Join(out, "capture-metadata.json")
	if err := os.WriteFile(extra, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(extra); err != nil || string(content) != "keep" {
		t.Fatalf("unrelated output changed: %v", err)
	}
	for name, original := range originals {
		content, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil || !bytes.Equal(content, original) {
			t.Errorf("rebuild changed deterministic output %s: %v", name, err)
		}
	}
	if err := os.WriteFile(readme, []byte("# Updated README\n\nFresh documentation from disk."), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Build(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	docs, err = os.ReadFile(filepath.Join(out, "docs.html"))
	if err != nil || !bytes.Contains(docs, []byte("Fresh documentation from disk.")) || bytes.Contains(docs, []byte("Formatted text")) {
		t.Fatalf("rebuild did not read the latest README: %v", err)
	}
}

func TestWriteFilesRefusesConflictBeforeWriting(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "index.html"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(out, map[string][]byte{"index.html": []byte("replace"), "a.html": []byte("new")}); err == nil {
		t.Fatal("expected output conflict")
	}
	if _, err := os.Stat(filepath.Join(out, "a.html")); !os.IsNotExist(err) {
		t.Fatal("wrote another output before detecting conflict")
	}
	content, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil || string(content) != "unrelated" {
		t.Fatal("overwrote unrelated file")
	}
}

func TestWriteFilesRejectsSymlinkAndUnsafePaths(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "actual")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(link, map[string][]byte{"index.html": {}}); err == nil {
		t.Fatal("accepted symlink output")
	}
	if err := os.Symlink(filepath.Join(target, "outside.html"), filepath.Join(target, "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(target, map[string][]byte{"index.html": {}}); err == nil {
		t.Fatal("accepted symlink output file")
	}
	for _, name := range []string{"../escape", "/absolute", "a/../b", "."} {
		if err := writeFiles(target, map[string][]byte{name: {}}); err == nil {
			t.Errorf("accepted unsafe path %q", name)
		}
	}
}

func TestWriteFilesResolvesSelectedParent(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "parent")
	if err := os.Symlink(actual, parent); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(filepath.Join(parent, "nested", "demo"), map[string][]byte{"index.html": []byte("demo")}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(actual, "nested", "demo", "index.html"))
	if err != nil || string(content) != "demo" {
		t.Fatalf("selected parent not resolved: %q, %v", content, err)
	}
}

func TestDemoScriptHasNoLiveServices(t *testing.T) {
	content, err := assets.ReadFile("assets/demo.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fetch(", "XMLHttpRequest", "WebSocket", "tileLayer", "localStorage", "sessionStorage", "document.cookie", "http://", "https://", "/api/"} {
		if bytes.Contains(content, []byte(forbidden)) {
			t.Errorf("demo script contains live behavior %q", forbidden)
		}
	}
}
