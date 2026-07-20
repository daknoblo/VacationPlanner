// Package i18n provides a tiny, dependency-free translation layer with a
// cookie/Accept-Language based locale resolver. English is the fallback locale.
package i18n

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Lang is a supported UI language code.
type Lang string

const (
	// LangEN is English (the fallback locale).
	LangEN Lang = "en"
	// LangDE is German.
	LangDE Lang = "de"
)

// DefaultLang is used when no cookie or Accept-Language match is found.
const DefaultLang = LangEN

// CookieName stores the user's language preference.
const CookieName = "lang"

// supported lists the selectable languages in display order.
var supported = []Lang{LangEN, LangDE}

// Supported returns the selectable languages in display order.
func Supported() []Lang { return supported }

// ParseLang converts a string such as "en" or "de-DE" into a supported Lang.
func ParseLang(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_;,"); i >= 0 {
		s = s[:i]
	}
	switch Lang(s) {
	case LangEN:
		return LangEN, true
	case LangDE:
		return LangDE, true
	default:
		return "", false
	}
}

// Localizer resolves message keys for a single language.
type Localizer struct {
	lang Lang
}

// NewLocalizer builds a Localizer for the given language.
func NewLocalizer(lang Lang) *Localizer {
	if _, ok := ParseLang(string(lang)); !ok {
		lang = DefaultLang
	}
	return &Localizer{lang: lang}
}

// Lang returns the active language.
func (l *Localizer) Lang() Lang { return l.lang }

// Code returns the active language as a plain string (for the html lang attr).
func (l *Localizer) Code() string { return string(l.lang) }

// Is reports whether the active language equals lang.
func (l *Localizer) Is(lang Lang) bool { return l.lang == lang }

// T translates a key. Missing keys fall back to English and finally to the key
// itself. Optional args are applied via fmt.Sprintf.
func (l *Localizer) T(key string, args ...any) string {
	msg, ok := messages[l.lang][key]
	if !ok {
		msg, ok = messages[DefaultLang][key]
		if !ok {
			return key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

type ctxKey struct{}

// NewContext stores a Localizer in the context.
func NewContext(ctx context.Context, l *Localizer) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the Localizer stored in the context, or a default one.
func FromContext(ctx context.Context) *Localizer {
	if l, ok := ctx.Value(ctxKey{}).(*Localizer); ok {
		return l
	}
	return NewLocalizer(DefaultLang)
}

// FromRequest resolves the language from the lang cookie, then the
// Accept-Language header, then the default.
func FromRequest(r *http.Request) *Localizer {
	if c, err := r.Cookie(CookieName); err == nil {
		if lang, ok := ParseLang(c.Value); ok {
			return &Localizer{lang: lang}
		}
	}
	if lang, ok := fromAcceptLanguage(r.Header.Get("Accept-Language")); ok {
		return &Localizer{lang: lang}
	}
	return NewLocalizer(DefaultLang)
}

// SetLangCookie persists the language preference for one year.
func SetLangCookie(w http.ResponseWriter, lang Lang, secure bool) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // language preference cookie; not security-sensitive, Secure set in production #nosec G124
		Name:     CookieName,
		Value:    string(lang),
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func fromAcceptLanguage(header string) (Lang, bool) {
	for _, part := range strings.Split(header, ",") {
		if lang, ok := ParseLang(part); ok {
			return lang, true
		}
	}
	return "", false
}
