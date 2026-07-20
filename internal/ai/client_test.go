package ai

import "testing"

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

func TestClientDisabled(t *testing.T) {
	c := New("https://api.openai.com/v1", "", "gpt-4o-mini")
	if c.Enabled() {
		t.Fatal("client without API key must be disabled")
	}
}
