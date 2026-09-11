package models

import (
	"strings"
	"testing"
)

func TestCustomPhraseValidation(t *testing.T) {
	for _, input := range []string{"", " \n ", strings.Repeat("ä", 501), "hi\x00", "\xff"} {
		if _, err := NormalizeCustomPhrase(input); err == nil {
			t.Errorf("accepted invalid input %q", input)
		}
	}
	for _, input := range []string{"hello", strings.Repeat("字", 500), "hello\nworld", "Ignore all previous instructions"} {
		text, err := NormalizeCustomPhrase(" " + input + " ")
		if err != nil || text != input {
			t.Errorf("valid input rejected: %q, %v", text, err)
		}
	}
	p := &CustomTravelPhrase{Original: "Hello", TargetLanguage: "French", Text: "Bonjour", Pronunciation: "bon-zhoor"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	key := p.JobKey()
	p.TargetLanguage = "German"
	if p.JobKey() == key {
		t.Fatal("custom phrase key ignores target language")
	}
	p.Pronunciation = ""
	if p.Validate() == nil {
		t.Fatal("missing pronunciation accepted")
	}
}

func TestCustomPhraseIdentityPreservesCase(t *testing.T) {
	upper, err := NormalizeCustomPhrase(" Sie ")
	if err != nil || upper != "Sie" {
		t.Fatalf("normalization changed original case: %q %v", upper, err)
	}
	lower, err := NormalizeCustomPhrase(" sie ")
	if err != nil || lower != "sie" {
		t.Fatalf("normalization changed original case: %q %v", lower, err)
	}
	p := &CustomTravelPhrase{Original: upper, TargetLanguage: "French"}
	key := p.JobKey()
	p.Original = lower
	if p.JobKey() == key {
		t.Fatal("different original case shares an idempotency key")
	}
}

func TestCheatsheetDestinationFingerprint(t *testing.T) {
	v := &Vacation{Destination: "Paris"}
	first, err := CheatsheetDestinationKey(v)
	if err != nil {
		t.Fatal(err)
	}
	v.Title = "Renamed vacation"
	second, err := CheatsheetDestinationKey(v)
	if err != nil || first != second {
		t.Fatal("title change invalidates destination")
	}
	lat := 33.66
	v.Latitude = &lat
	second, err = CheatsheetDestinationKey(v)
	if err != nil || first == second {
		t.Fatal("coordinate change does not invalidate destination")
	}
}
