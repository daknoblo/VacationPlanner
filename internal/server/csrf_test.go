package server

import (
	"testing"

	"github.com/daknoblo/vacationplanner/internal/config"
)

func testServer() *Server {
	return &Server{cfg: &config.Config{
		Env:     "test",
		CSRFKey: []byte("0123456789abcdef0123456789abcdef"),
	}}
}

func TestCSRFTokenRoundTrip(t *testing.T) {
	s := testServer()
	token := s.newCSRFToken()
	if !s.validCSRFToken(token) {
		t.Fatal("freshly issued token should be valid")
	}
}

func TestCSRFTokenRejectsTampering(t *testing.T) {
	s := testServer()
	token := s.newCSRFToken()

	// Flip the first character to invalidate the signature. The first base64
	// character is always fully significant, unlike a trailing character whose
	// low bits are padding (flipping which can decode to the same bytes).
	var bad string
	if token[0] == 'A' {
		bad = "B" + token[1:]
	} else {
		bad = "A" + token[1:]
	}
	if s.validCSRFToken(bad) {
		t.Fatal("tampered token must be rejected")
	}

	if s.validCSRFToken("") || s.validCSRFToken("not-a-token") || s.validCSRFToken("a.b") {
		t.Fatal("malformed tokens must be rejected")
	}
}

func TestCSRFTokenWrongKey(t *testing.T) {
	s1 := testServer()
	s2 := &Server{cfg: &config.Config{CSRFKey: []byte("ffffffffffffffffffffffffffffffff")}}

	token := s1.newCSRFToken()
	if s2.validCSRFToken(token) {
		t.Fatal("token signed with a different key must be rejected")
	}
}
