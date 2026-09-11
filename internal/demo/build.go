package demo

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/daknoblo/vacationplanner/internal/server"
	"github.com/daknoblo/vacationplanner/web"
)

//go:embed assets
var assets embed.FS

const generator = "vacationplanner-static-demo-v1"

// Options controls the destination and the version displayed in generated pages.
type Options struct {
	Out     string
	Version string
	Readme  string
}

// Manifest describes deterministic builder-owned outputs. Screenshot generation
// can add files alongside these without giving the builder ownership of them.
type Manifest struct {
	Generator string   `json:"generator"`
	Version   string   `json:"version"`
	Languages []string `json:"languages"`
	Pages     []string `json:"pages"`
	Files     []string `json:"files"`
	Synthetic bool     `json:"synthetic"`
	ReadOnly  bool     `json:"readOnly"`
}

// Build renders both locales without contacting any external services.
func Build(ctx context.Context, options Options) error {
	if options.Out == "" {
		return fmt.Errorf("demo output directory must not be empty")
	}
	files := map[string][]byte{".nojekyll": {}}
	languages := []string{"en", "de"}
	pages := []string{"index.html", "vacation.html", "settings.html", "about.html", "export.html"}
	for _, language := range languages {
		snapshot, err := server.RenderDemo(ctx, language, options.Version)
		if err != nil {
			return fmt.Errorf("render %s demo: %w", language, err)
		}
		for _, name := range pages {
			raw, ok := snapshot.Pages[name]
			if !ok {
				return fmt.Errorf("demo snapshot missing %s page %q", language, name)
			}
			content, err := transform(raw, snapshot.Fragments, snapshot.TripID, language, name)
			if err != nil {
				return fmt.Errorf("transform %s/%s: %w", language, name, err)
			}
			files["demo/"+language+"/"+name] = content
		}
		var mapData any
		if err := json.Unmarshal(snapshot.MapData, &mapData); err != nil {
			return fmt.Errorf("invalid %s demo map data: %w", language, err)
		}
		// Marshal escapes HTML delimiters and rejects malformed JSON before it
		// becomes executable JavaScript. It contains synthetic coordinates only.
		encoded, err := json.Marshal(mapData)
		if err != nil {
			return err
		}
		files["demo/"+language+"/map-data.js"] = append(append([]byte("window.VP_DEMO_MAP = "), encoded...), []byte(";\n")...)
	}
	if err := copyAssets(files, web.Static, "static", "static", func(name string) bool {
		return !strings.HasPrefix(name, "static/js/") && !strings.HasPrefix(name, "static/vendor/htmx/")
	}); err != nil {
		return err
	}
	if err := copyAssets(files, assets, "assets", "static/demo", nil); err != nil {
		return err
	}
	var readme []byte
	if options.Readme != "" {
		content, err := os.ReadFile(options.Readme)
		if err != nil {
			return fmt.Errorf("read documentation: %w", err)
		}
		readme, err = renderReadme(content)
		if err != nil {
			return fmt.Errorf("render documentation: %w", err)
		}
	}
	data := struct {
		Version   string
		Readme    template.HTML
		Languages []string
	}{
		Version:   options.Version,
		Readme:    template.HTML(readme), //nolint:gosec // G203: Goldmark disables raw HTML; renderReadme sanitizes URLs and attributes.
		Languages: languages,
	}
	for name, source := range map[string]string{"index.html": landingTemplate, "docs.html": docsTemplate} {
		tmpl, err := template.New(name).Parse(source)
		if err != nil {
			return err
		}
		var b bytes.Buffer
		if err := tmpl.Execute(&b, data); err != nil {
			return err
		}
		files[name] = b.Bytes()
	}
	manifest := Manifest{Generator: generator, Version: options.Version, Languages: languages, Pages: pages, Synthetic: true, ReadOnly: true}
	for name := range files {
		manifest.Files = append(manifest.Files, name)
	}
	manifest.Files = append(manifest.Files, "manifest.json")
	sort.Strings(manifest.Files)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	files["manifest.json"] = append(encoded, '\n')
	return writeFiles(options.Out, files)
}

func copyAssets(files map[string][]byte, source fs.FS, root, target string, include func(string) bool) error {
	return fs.WalkDir(source, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if include != nil && !include(name) {
			return nil
		}
		content, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		files[target+strings.TrimPrefix(name, root)] = content
		return nil
	})
}

func safePath(name string) bool {
	return name != "." && !filepath.IsAbs(name) && filepath.ToSlash(filepath.Clean(name)) == name &&
		name != ".." && !strings.HasPrefix(name, "../")
}

func rejectSymlinks(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("demo output must not traverse symlink %q", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

// Resolve the operator-selected parent path (including macOS /var and /tmp),
// while still refusing an output root that is itself a symbolic link.
func canonicalOutputRoot(out string) (string, error) {
	absolute, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	if info, err := os.Lstat(absolute); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("demo output must not be a symlink %q", absolute)
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	suffix := []string{filepath.Base(absolute)}
	for parent := filepath.Dir(absolute); ; parent = filepath.Dir(parent) {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			return filepath.Join(append([]string{resolved}, suffix...)...), nil
		}
		if !os.IsNotExist(err) || parent == filepath.Dir(parent) {
			return "", err
		}
		suffix = append([]string{filepath.Base(parent)}, suffix...)
	}
}

// Existing unrelated files are never replaced or deleted. All conflicts are
// checked before writing any output; reruns may replace only manifest-owned files.
func writeFiles(out string, files map[string][]byte) error {
	var err error
	out, err = canonicalOutputRoot(out)
	if err != nil {
		return err
	}
	if err := rejectSymlinks(out); err != nil {
		return err
	}
	owned := map[string]bool{}
	manifestPath := filepath.Join(out, "manifest.json")
	if err := rejectSymlinks(manifestPath); err != nil {
		return err
	}
	// The build operator chooses the output root; symlink traversal was rejected above.
	if raw, err := os.ReadFile(manifestPath); err == nil { //nolint:gosec // G304: intentional local build manifest.
		var previous Manifest
		if err := json.Unmarshal(raw, &previous); err != nil || previous.Generator != generator {
			return fmt.Errorf("refusing to overwrite unrelated demo manifest %q", manifestPath)
		}
		for _, name := range previous.Files {
			if !safePath(name) {
				return fmt.Errorf("unsafe path in previous demo manifest: %q", name)
			}
			owned[name] = true
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if !safePath(name) {
			return fmt.Errorf("unsafe demo output path %q", name)
		}
		target := filepath.Join(out, filepath.FromSlash(name))
		if err := rejectSymlinks(target); err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil {
			if !info.Mode().IsRegular() || !owned[name] {
				return fmt.Errorf("refusing to overwrite unrelated output %q; choose a fresh -out directory", target)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		// Detect parent file conflicts before any writes.
		for parent := filepath.Dir(target); parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
			if info, err := os.Stat(parent); err == nil && !info.IsDir() {
				return fmt.Errorf("demo output parent is not a directory: %q", parent)
			}
		}
		names = append(names, name)
	}
	sort.Strings(names)
	// Write the ownership manifest last, after the complete site exists.
	names = appendExceptManifest(names)
	for _, name := range names {
		target := filepath.Join(out, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // G301: public static site directories.
			return err
		}
		if err := os.WriteFile(target, files[name], 0o644); err != nil { //nolint:gosec // G306: public, synthetic static assets only.
			return err
		}
	}
	return nil
}

func appendExceptManifest(names []string) []string {
	result := make([]string, 0, len(names))
	found := false
	for _, name := range names {
		if name == "manifest.json" {
			found = true
		} else {
			result = append(result, name)
		}
	}
	if found {
		result = append(result, "manifest.json")
	}
	return result
}

const siteHead = `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'"><title>VacationPlanner · {{.Version}}</title><link rel="stylesheet" href="static/css/app.css"><link rel="stylesheet" href="static/demo/demo.css"></head><body class="demo-site"><header class="demo-banner"><strong>VacationPlanner · {{.Version}}</strong><span>Documentation and synthetic, read-only demo. Nothing is saved. No external services are called.</span><nav><a href="index.html">Home &amp; gallery</a><a href="docs.html">Documentation</a><a href="demo/en/index.html">English demo</a><a href="demo/de/index.html">Deutsche Demo</a><a href="https://github.com/daknoblo/VacationPlanner">GitHub</a></nav></header><main class="container">`

const landingTemplate = siteHead + `
<section class="panel demo-hero"><p class="muted">Private trip planning · Go · SQLite · HTMX · Leaflet</p><h1>Your whole trip, in one place.</h1><p>Explore a complete itinerary with accommodation, day and week planners, ideas, budgets and a travel cheatsheet. These pages use the actual application templates and styles, with all changes and live services disabled.</p><p><a class="btn btn--primary" href="demo/en/vacation.html">Explore the demo</a> <a class="btn" href="demo/de/vacation.html">Demo auf Deutsch</a> <a class="btn" href="docs.html">Read the documentation</a></p><p class="muted">Maps, landscape artwork and route metrics are illustrative fixtures, not navigation data.</p></section>
<section class="panel"><h2>Application gallery</h2><p>Automated screenshots of the same synthetic itinerary, in English and German.</p>
{{range .Languages}}<h3>{{if eq . "en"}}English{{else}}Deutsch{{end}}</h3><div class="demo-gallery">
<a href="demo/{{.}}/index.html"><img src="screenshots/{{.}}/dashboard.png" alt="Dashboard with planned trips" loading="lazy"><strong>Dashboard</strong></a>
<a href="demo/{{.}}/vacation.html#overview"><img src="screenshots/{{.}}/overview.png" alt="Trip overview and illustrative accommodation map" loading="lazy"><strong>Overview</strong></a>
<a href="demo/{{.}}/vacation.html#day-0"><img src="screenshots/{{.}}/day-planner.png" alt="Day planner and illustrative route summary" loading="lazy"><strong>Day planner</strong></a>
<a href="demo/{{.}}/vacation.html#week"><img src="screenshots/{{.}}/week-planner.png" alt="Week calendar" loading="lazy"><strong>Week planner</strong></a>
<a href="demo/{{.}}/vacation.html#ideen"><img src="screenshots/{{.}}/ideas.png" alt="Saved activity ideas" loading="lazy"><strong>Ideas</strong></a>
<a href="demo/{{.}}/vacation.html#budget"><img src="screenshots/{{.}}/budget.png" alt="Trip budget" loading="lazy"><strong>Budget</strong></a>
<a href="demo/{{.}}/vacation.html#cheatsheet"><img src="screenshots/{{.}}/cheatsheet.png" alt="Travel cheatsheet" loading="lazy"><strong>Cheatsheet</strong></a>
<a href="demo/{{.}}/vacation.html#travel"><img src="screenshots/{{.}}/travel.png" alt="Arrival and departure planning" loading="lazy"><strong>Travel</strong></a>
<a href="demo/{{.}}/vacation.html#lodging"><img src="screenshots/{{.}}/accommodation.png" alt="Accommodation bookings" loading="lazy"><strong>Accommodation</strong></a>
<a href="demo/{{.}}/settings.html"><img src="screenshots/{{.}}/settings.png" alt="Read-only application settings" loading="lazy"><strong>Settings</strong></a>
<a href="demo/{{.}}/about.html"><img src="screenshots/{{.}}/about.png" alt="About VacationPlanner" loading="lazy"><strong>About</strong></a>
<a href="demo/{{.}}/vacation.html#overview"><img src="screenshots/{{.}}/mobile.png" alt="Mobile trip overview" loading="lazy"><strong>Mobile</strong></a>
</div>{{end}}</section></main></body></html>`

const docsTemplate = siteHead + `<article class="panel"><p class="muted">Generated from the current repository README for this build. <a href="https://github.com/daknoblo/VacationPlanner/blob/main/README.md">View the source on GitHub</a>.</p><div class="demo-readme">{{.Readme}}</div></article></main></body></html>`
