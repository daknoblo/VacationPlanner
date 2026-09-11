// Package demo builds an offline, read-only showcase from the application's real UI.
package demo

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

const staticPrefix = "../../static/"

type labels struct {
	banner, disabled, mapNote, docs string
}

func locale(language string) labels {
	if language == "de" {
		return labels{
			"Demo · Synthetische Beispieldaten · Nur lesen. Nichts wird gespeichert. Keine KI-, Geocoding- oder Routing-Aufrufe.",
			"In dieser statischen Demo nicht verfügbar; Änderungen werden nicht gespeichert.",
			"Illustrative Karte und Routenwerte – keine echte Navigation oder Live-Kartendaten.",
			"Dokumentation",
		}
	}
	return labels{
		"Demo · Synthetic example data · Read only. Nothing is saved. No AI, geocoding or routing calls.",
		"Unavailable in this static demo; changes are not saved.",
		"Illustrative map and route metrics — not real navigation or live map data.",
		"Documentation",
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func setAttr(n *html.Node, key, value string) {
	for i := range n.Attr {
		if n.Attr[i].Key == key {
			n.Attr[i].Val = value
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: value})
}

func element(tag, text string, attrs ...string) *html.Node {
	n := &html.Node{Type: html.ElementNode, Data: tag}
	for i := 0; i+1 < len(attrs); i += 2 {
		setAttr(n, attrs[i], attrs[i+1])
	}
	if text != "" {
		n.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	}
	return n
}

func find(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := find(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// Hydration runs before sanitization: nested GET fragments become ordinary markup.
// A cycle is an error rather than leaving a live placeholder in the published site.
func hydrate(n *html.Node, fragments map[string][]byte, stack map[string]bool, depth int) error {
	if depth > 32 {
		return fmt.Errorf("demo fragment nesting exceeds 32")
	}
	key := attr(n, "hx-get")
	if key != "" && n.Data != "button" && n.Data != "a" && n.Data != "form" {
		content, ok := fragments[key]
		if !ok {
			return fmt.Errorf("missing demo fragment %q", key)
		}
		if stack[key] {
			return fmt.Errorf("cyclic demo fragment %q", key)
		}
		stack[key] = true
		parsed, err := html.ParseFragment(bytes.NewReader(content), n)
		if err != nil {
			return fmt.Errorf("parse demo fragment %q: %w", key, err)
		}
		for n.FirstChild != nil {
			n.RemoveChild(n.FirstChild)
		}
		for _, child := range parsed {
			n.AppendChild(child)
		}
		defer delete(stack, key)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if err := hydrate(c, fragments, stack, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func localAsset(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" {
		return ""
	}
	clean := path.Clean(u.Path)
	if !strings.HasPrefix(clean, "/static/") {
		return ""
	}
	return staticPrefix + strings.TrimPrefix(clean, "/static/")
}

func navigation(raw, tripID string) string {
	if strings.HasPrefix(raw, "#") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		return raw // Explicit user-clicked links only; never used for resource loading.
	}
	if u.Scheme != "" || u.Host != "" {
		return ""
	}
	switch u.Path {
	case "/", "/vacations":
		return "index.html"
	case "/settings":
		return "settings.html"
	case "/about":
		return "about.html"
	case "/vacations/" + tripID:
		if u.Fragment != "" {
			return "vacation.html#" + url.PathEscape(u.Fragment)
		}
		return "vacation.html"
	case "/vacations/" + tripID + "/export":
		return "export.html"
	}
	return ""
}

func readonlyControl(n *html.Node) bool {
	for _, key := range []string{"data-tab", "data-view", "data-goto-day", "data-payer-filter", "data-print", "data-ideas-region-filter"} {
		if hasAttr(n, key) {
			return true
		}
	}
	return false
}

func keepData(key string) bool {
	switch key {
	case "data-tabs", "data-tab", "data-tab-panel", "data-view", "data-viewtoggle",
		"data-goto-day", "data-day-view", "data-weekview", "data-tagesplan",
		"data-day-count", "data-day", "data-day-route", "data-week-start",
		"data-ideas-regions", "data-ideas-region-filter", "data-ideas-region-group",
		"data-ideas-region-empty", "data-budget-filter", "data-payer-filter", "data-payer",
		"data-id", "data-travel-source-id",
		"data-print", "data-focus-map", "data-lat", "data-lng", "data-zoom":
		return true
	}
	return false
}

func sanitize(n *html.Node, tripID string, text labels) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		drop := false
		if c.Type == html.CommentNode {
			drop = true
		}
		if attr(c, "id") == "toast" {
			drop = true
		}
		if c.Type == html.ElementNode {
			switch c.Data {
			case "script", "iframe", "object", "embed", "base", "audio", "video", "source":
				drop = true
			case "meta":
				drop = strings.Contains(strings.ToLower(attr(c, "name")), "csrf") || hasAttr(c, "http-equiv")
			case "input":
				drop = attr(c, "type") == "hidden"
			case "link":
				resource := localAsset(attr(c, "href"))
				drop = attr(c, "rel") != "stylesheet" || resource == ""
				if !drop {
					setAttr(c, "href", resource)
				}
			}
			if !drop {
				if c.Data == "img" {
					resource := localAsset(attr(c, "src"))
					if resource == "" {
						resource = staticPrefix + "demo/landscape.svg"
					}
					setAttr(c, "src", resource)
				}
				if c.Data == "a" {
					target := navigation(attr(c, "href"), tripID)
					if target == "" {
						setAttr(c, "aria-disabled", "true")
						setAttr(c, "title", text.disabled)
					}
					setAttr(c, "href", target)
				}
				if c.Data == "form" {
					c.Data = "div"
					c.DataAtom = 0
					setAttr(c, "aria-label", text.disabled)
				}
				if c.Data == "input" || c.Data == "textarea" || c.Data == "select" || c.Data == "button" {
					if !readonlyControl(c) {
						setAttr(c, "disabled", "")
						setAttr(c, "title", text.disabled)
					}
					if c.Data == "button" {
						setAttr(c, "type", "button")
					}
					name := strings.ToLower(attr(c, "name") + " " + attr(c, "id"))
					if attr(c, "type") == "password" || strings.Contains(name, "secret") ||
						strings.Contains(name, "token") || strings.Contains(name, "api_key") || strings.Contains(name, "apikey") {
						setAttr(c, "value", "")
						for c.FirstChild != nil {
							c.RemoveChild(c.FirstChild)
						}
					}
				}
				attrs := c.Attr[:0]
				for _, a := range c.Attr {
					key := strings.ToLower(a.Key)
					if strings.HasPrefix(key, "hx-") || strings.HasPrefix(key, "data-hx-") ||
						strings.HasPrefix(key, "on") || (strings.HasPrefix(key, "data-") && !keepData(key)) ||
						key == "action" || key == "method" || key == "formaction" || key == "form" ||
						key == "srcset" || key == "ping" || key == "draggable" || key == "autofocus" ||
						key == "contenteditable" || key == "download" || key == "background" ||
						(key == "href" && a.Val == "") ||
						(key == "style" && (strings.Contains(strings.ToLower(a.Val), "url(") || strings.Contains(a.Val, "@"))) {
						continue
					}
					attrs = append(attrs, a)
				}
				c.Attr = attrs
			}
		}
		if drop {
			n.RemoveChild(c)
		} else {
			sanitize(c, tripID, text)
		}
		c = next
	}
}

func transform(raw []byte, fragments map[string][]byte, tripID, language, filename string) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	if err := hydrate(doc, fragments, map[string]bool{}, 0); err != nil {
		return nil, err
	}
	text := locale(language)
	sanitize(doc, tripID, text)
	head, body := find(doc, "head"), find(doc, "body")
	if head == nil || body == nil {
		return nil, fmt.Errorf("demo page lacks head or body")
	}
	head.AppendChild(element("meta", "", "http-equiv", "Content-Security-Policy", "content",
		"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"))
	head.AppendChild(element("link", "", "rel", "stylesheet", "href", staticPrefix+"demo/demo.css"))
	banner := element("aside", "", "class", "demo-banner", "aria-label", "Demo")
	banner.AppendChild(element("strong", text.banner))
	banner.AppendChild(element("span", text.mapNote))
	nav := element("nav", "", "aria-label", "Demo")
	nav.AppendChild(element("a", "English", "href", "../en/"+filename, "lang", "en"))
	nav.AppendChild(element("a", "Deutsch", "href", "../de/"+filename, "lang", "de"))
	nav.AppendChild(element("a", text.docs, "href", "../../docs.html"))
	banner.AppendChild(nav)
	body.InsertBefore(banner, body.FirstChild)
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		addMapNotes(c, text.mapNote)
	}
	body.AppendChild(element("script", "", "src", staticPrefix+"vendor/leaflet/leaflet.js"))
	body.AppendChild(element("script", "", "src", "map-data.js"))
	body.AppendChild(element("script", "", "src", staticPrefix+"demo/demo.js"))
	var out bytes.Buffer
	err = html.Render(&out, doc)
	return out.Bytes(), err
}

func addMapNotes(n *html.Node, note string) {
	if attr(n, "id") == "map" || hasAttr(n, "data-day-route") {
		n.Parent.InsertBefore(element("p", note, "class", "demo-map-note"), n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		addMapNotes(c, note)
	}
}
