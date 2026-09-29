package ai

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestDescribeIdeaBoundedBilingualResponse(t *testing.T) {
	for _, value := range []string{
		`{"en":"A reconstructed Viking harbour.","de":"Ein rekonstruierter Wikingerhafen."}`,
		`{"en":"","de":""}`,
		`{"en":"<b>Museum</b>","de":"Museum"}`,
		`{"en":"` + strings.Repeat("x", 161) + `","de":"Museum"}`,
		`{"en":"Museum\nDetails","de":"Museum"}`,
		`not JSON`,
	} {
		body, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": value}}}})
		if err != nil {
			t.Fatal(err)
		}
		backend := &fakeFoundry{response: string(body)}
		job := &models.IdeaDescription{Title: "Bork Vikingehavn", Destination: "Denmark"}
		err = New(backend).DescribeIdea(t.Context(), "production-chat", job)
		valid := strings.Contains(value, "reconstructed")
		if (err == nil) != valid || backend.calls != 1 {
			t.Fatalf("response %s: %v", value, err)
		}
		if valid && (job.German == "" || !strings.Contains(string(backend.payload), `"max_completion_tokens":512`)) {
			t.Fatal("missing translation or unbounded request")
		}
	}
	if err := New(nil).DescribeIdea(t.Context(), "", &models.IdeaDescription{}); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}
