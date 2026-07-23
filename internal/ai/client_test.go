package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSuggestionsObject(t *testing.T) {
	in := `{"suggestions":[{"name":"Castelo","category":"Burg","description":"d","reason":"r"}]}`
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Castelo" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestParseSuggestionsArray(t *testing.T) {
	in := `[{"name":"A"},{"name":"B"}]`
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestParseSuggestionsFenced(t *testing.T) {
	in := "```json\n{\"suggestions\":[{\"name\":\"A\"}]}\n```"
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestParseSuggestionsInvalid(t *testing.T) {
	if _, err := parseSuggestions("totally not json"); err == nil {
		t.Fatal("expected error for invalid content")
	}
}

func TestParseSuggestionsEmbedded(t *testing.T) {
	in := "Sure! Here are some ideas for you:\n{\"suggestions\":[{\"name\":\"Sé\"}]}\nHope that helps!"
	got, err := parseSuggestions(in)
	if err != nil {
		t.Fatalf("parseSuggestions: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Sé" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestClientDisabled(t *testing.T) {
	c := New("")
	if c.Enabled() {
		t.Fatal("client without API key must be disabled")
	}
}

func TestDoChatErrorSurfacesEndpointAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"The model gpt-x does not exist"}}`))
	}))
	defer srv.Close()

	c := New("test-key")
	_, err := c.doChat(context.Background(), srv.URL, "gpt-x", "",
		[]chatMessage{{Role: "user", Content: "hi"}}, 0.5)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	msg := err.Error()
	for _, want := range []string{"404", "gpt-x", "does not exist", srv.URL} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q should contain %q", msg, want)
		}
	}
}
