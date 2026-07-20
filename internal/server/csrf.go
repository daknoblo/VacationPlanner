package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const csrfCtxKey ctxKey = iota

const (
	csrfCookieName = "csrf_token"
	csrfHeaderName = "X-CSRF-Token"
	csrfFormField  = "csrf_token"
	csrfMaxAge     = 24 * time.Hour
)

// csrf implements stateless, signed double-submit CSRF protection. Unsafe
// requests must present a token (via header or form field) whose HMAC signature
// was issued by this server. Safe requests receive a fresh token cookie.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.ensureCSRFToken(w, r)
		ctx := context.WithValue(r.Context(), csrfCtxKey, token)
		r = r.WithContext(ctx)

		if isUnsafeMethod(r.Method) {
			provided := r.Header.Get(csrfHeaderName)
			if provided == "" {
				provided = r.PostFormValue(csrfFormField)
			}
			if !s.validCSRFToken(provided) {
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ensureCSRFToken returns a valid token, reusing the cookie when possible and
// otherwise issuing and setting a fresh one.
func (s *Server) ensureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && s.validCSRFToken(c.Value) {
		return c.Value
	}
	token := s.newCSRFToken()
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // CSRF cookie must be JS-readable (double-submit); Secure is set in production #nosec G124
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(csrfMaxAge.Seconds()),
		HttpOnly: false, // must be readable by the front-end to echo it back
		Secure:   s.cfg.IsProduction(),
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

func (s *Server) newCSRFToken() string {
	payload := make([]byte, 16+8)
	_, _ = rand.Read(payload[:16])
	binary.BigEndian.PutUint64(payload[16:], uint64(time.Now().Unix())) //nolint:gosec // unix seconds are non-negative and fit within uint64 #nosec G115

	mac := s.csrfMAC(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac)
}

func (s *Server) validCSRFToken(token string) bool {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) != 24 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, s.csrfMAC(payload)) {
		return false
	}
	issued := int64(binary.BigEndian.Uint64(payload[16:])) //nolint:gosec // unix timestamp round-trips within int64 range #nosec G115
	return time.Since(time.Unix(issued, 0)) <= csrfMaxAge
}

func (s *Server) csrfMAC(payload []byte) []byte {
	m := hmac.New(sha256.New, s.cfg.CSRFKey)
	m.Write(payload)
	return m.Sum(nil)
}

func csrfToken(ctx context.Context) string {
	if v, ok := ctx.Value(csrfCtxKey).(string); ok {
		return v
	}
	return ""
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
