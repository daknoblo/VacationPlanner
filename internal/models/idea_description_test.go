package models

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestShortDescription(t *testing.T) {
	for input, want := range map[string]string{
		"": "", "  A museum. More detail! ": "A museum.",
		"A  lake\nnear town":               "A lake near town",
		"An excursion 12.3 km away. More.": "An excursion 12.3 km away.",
	} {
		if got := ShortDescription(input); got != want {
			t.Fatalf("%q: got %q, want %q", input, got, want)
		}
	}
	got := ShortDescription(strings.Repeat("Grüne Küste ", 30))
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 160 || !strings.HasSuffix(got, "\u2026") {
		t.Fatal("invalid bounded excerpt", got)
	}
}
