package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseLang(t *testing.T) {
	cases := map[string]struct {
		want Lang
		ok   bool
	}{
		"en":    {LangEN, true},
		"de":    {LangDE, true},
		"de-DE": {LangDE, true},
		"EN":    {LangEN, true},
		"fr":    {"", false},
		"":      {"", false},
	}
	for in, exp := range cases {
		got, ok := ParseLang(in)
		if ok != exp.ok || got != exp.want {
			t.Errorf("ParseLang(%q) = %q,%v; want %q,%v", in, got, ok, exp.want, exp.ok)
		}
	}
}

func TestLocalizerT(t *testing.T) {
	en := NewLocalizer(LangEN)
	de := NewLocalizer(LangDE)

	if got := en.T("nav.vacations"); got != "My vacations" {
		t.Errorf("en nav.vacations = %q", got)
	}
	if got := de.T("nav.vacations"); got != "Meine Urlaube" {
		t.Errorf("de nav.vacations = %q", got)
	}
	if got := en.T("does.not.exist"); got != "does.not.exist" {
		t.Errorf("missing key should return the key, got %q", got)
	}
}

func TestLocalizerCodeAndFallback(t *testing.T) {
	if NewLocalizer(LangDE).Code() != "de" {
		t.Error("Code() should be de")
	}
	if NewLocalizer("xx").Lang() != DefaultLang {
		t.Error("invalid lang should fall back to the default")
	}
}

func TestFromRequestCookie(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "de"})
	if FromRequest(r).Lang() != LangDE {
		t.Error("cookie de should resolve to LangDE")
	}
}

func TestFromRequestAcceptLanguage(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "de-DE,de;q=0.9,en;q=0.8")
	if FromRequest(r).Lang() != LangDE {
		t.Error("Accept-Language de should resolve to LangDE")
	}
}

func TestFromRequestDefault(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	if FromRequest(r).Lang() != DefaultLang {
		t.Error("no signal should resolve to the default")
	}
}

// TestCatalogsHaveSameKeys ensures every non-default locale defines exactly the
// same keys as the English fallback (no missing or stray translations).
func TestCatalogsHaveSameKeys(t *testing.T) {
	en := messages[LangEN]
	for lang, m := range messages {
		if lang == LangEN {
			continue
		}
		for k := range en {
			if _, ok := m[k]; !ok {
				t.Errorf("locale %q is missing key %q", lang, k)
			}
		}
		for k := range m {
			if _, ok := en[k]; !ok {
				t.Errorf("locale %q has extra key %q not present in en", lang, k)
			}
		}
	}
}
