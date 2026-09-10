package models

import (
	"errors"
	"net/url"
	"strings"
)

// ItemLink preserves a recommendation's external reference independently of edits.
type ItemLink struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// SafeExternalURL permits browser links, never executable schemes or credentials.
func SafeExternalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.String()
}

func (l ItemLink) SafeURL() string { return SafeExternalURL(l.URL) }

func ValidateItemLinks(links []ItemLink) error {
	if len(links) > 8 {
		return errors.New("item has too many external links")
	}
	for _, link := range links {
		switch link.Kind {
		case "website", "wikipedia", "tripadvisor", "google_maps", "trivago":
		default:
			return errors.New("item link has an unsupported kind")
		}
		if link.SafeURL() == "" {
			return errors.New("item link must use a valid HTTP or HTTPS URL without credentials")
		}
	}
	return nil
}
