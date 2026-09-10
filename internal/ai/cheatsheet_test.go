package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestCheatsheetRequiresEveryPhraseAndUsesBoundedChat(t *testing.T) {
	sheet := models.Cheatsheet{Country: "France", Language: "French"}
	for _, meaning := range models.TravelPhraseMeanings() {
		sheet.Phrases = append(sheet.Phrases, models.TravelPhrase{Key: meaning.Key, Text: "bonjour", Pronunciation: "bon-zhoor"})
	}
	content, err := json.Marshal(sheet)
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeFoundry{target: foundry.Target{Deployment: "production-chat"}, response: string(response)}
	got, err := New(f).GenerateCheatsheet(context.Background(), "production-chat", &models.Vacation{Destination: "Paris, France"}, "de")
	if err != nil || len(got.Phrases) != len(models.TravelPhraseMeanings()) {
		t.Fatalf("cheatsheet = %+v, %v", got, err)
	}
	if !strings.Contains(string(f.payload), `"max_completion_tokens":4096`) || !strings.Contains(string(f.payload), "Paris, France") {
		t.Fatalf("wrong request: %s", f.payload)
	}
	sheet.Phrases[1].Key = sheet.Phrases[0].Key
	if sheet.Validate() == nil {
		t.Fatal("duplicate phrase accepted")
	}
	sheet.Phrases = sheet.Phrases[:len(sheet.Phrases)-1]
	if sheet.Validate() == nil {
		t.Fatal("incomplete phrase list accepted")
	}
}
