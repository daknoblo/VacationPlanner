package demo

import (
	"bytes"
	"net/url"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const projectSite = "https://daknoblo.github.io/VacationPlanner/"
const repositoryFiles = "https://github.com/daknoblo/VacationPlanner/blob/main/"

// renderReadme deliberately leaves Goldmark's unsafe HTML option disabled.
// Images get a second, offline-only allowlist: remote badges become their alt
// text and published screenshot URLs become local screenshot references.
func renderReadme(source []byte) ([]byte, error) {
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	)
	var rendered bytes.Buffer
	if err := markdown.Convert(source, &rendered); err != nil {
		return nil, err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(&rendered, root)
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		root.AppendChild(node)
	}
	sanitizeReadme(root)
	var output bytes.Buffer
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&output, child); err != nil {
			return nil, err
		}
	}
	return output.Bytes(), nil
}

func screenshotPath(raw string) string {
	raw = strings.TrimPrefix(raw, projectSite)
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 3 || parts[0] != "screenshots" || (parts[1] != "en" && parts[1] != "de") {
		return ""
	}
	switch parts[2] {
	case "dashboard.png", "overview.png", "day-planner.png", "week-planner.png", "ideas.png",
		"budget.png", "cheatsheet.png", "settings.png", "about.png", "mobile.png",
		"travel.png", "accommodation.png":
		return u.Path
	}
	return ""
}

func documentationLink(raw string) string {
	if strings.HasPrefix(raw, "#") {
		return raw
	}
	if strings.HasPrefix(raw, projectSite) {
		local := strings.TrimPrefix(raw, projectSite)
		if local == "" {
			return "index.html"
		}
		if local == "docs.html" || local == "index.html" || screenshotPath(local) != "" {
			return local
		}
		for _, lang := range []string{"en", "de"} {
			u, err := url.Parse(local)
			if err != nil {
				return ""
			}
			for _, page := range []string{"index.html", "vacation.html", "export.html", "settings.html", "about.html"} {
				if u.Path == "demo/"+lang+"/"+page {
					return u.String()
				}
			}
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host != "" && !u.IsAbs() {
		return ""
	}
	if u.IsAbs() {
		if (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			return u.String()
		}
		return ""
	}
	if u.Path == "" {
		return ""
	}
	if u.Path == "README.md" {
		u.Path = "docs.html"
		return u.String()
	}
	if screenshot := screenshotPath(raw); screenshot != "" {
		return screenshot
	}
	clean := path.Clean(u.Path)
	if !safePath(clean) || strings.HasPrefix(u.Path, "/") {
		return ""
	}
	base, _ := url.Parse(repositoryFiles)
	u.Path = clean
	return base.ResolveReference(u).String()
}

func sanitizeReadme(root *html.Node) {
	for child := root.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.CommentNode {
			root.RemoveChild(child)
			child = next
			continue
		}
		if child.Type == html.ElementNode {
			if child.Data == "img" {
				local := screenshotPath(attr(child, "src"))
				if local == "" {
					root.InsertBefore(&html.Node{Type: html.TextNode, Data: attr(child, "alt")}, child)
					root.RemoveChild(child)
					child = next
					continue
				}
				setAttr(child, "src", local)
				setAttr(child, "loading", "lazy")
			}
			if child.Data == "a" {
				setAttr(child, "href", documentationLink(attr(child, "href")))
			}
			attrs := child.Attr[:0]
			for _, a := range child.Attr {
				switch a.Key {
				case "id", "class", "title", "alt", "src", "loading", "align", "colspan", "rowspan", "start", "type", "checked", "disabled":
					attrs = append(attrs, a)
				case "href":
					if a.Val != "" {
						attrs = append(attrs, a)
					}
				}
			}
			child.Attr = attrs
		}
		sanitizeReadme(child)
		child = next
	}
}
