package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestIntroductionRequiresPreservedNamePlaceholder(t *testing.T) {
	for _, output := range []struct {
		text, pronunciation string
		valid               bool
	}{
		{"Jeg hedder {name}", "yai HED-er {name}", true},
		{"Jeg hedder Daniel", "yai HED-er Daniel", false},
		{"Jeg hedder {name}", "yai HED-er", false},
		{"{name} {name}", "{name}", false},
	} {
		content, err := json.Marshal(models.IntroductionPhrase{Text: output.text, Pronunciation: output.pronunciation})
		if err != nil {
			t.Fatal(err)
		}
		response, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(content)}}}})
		if err != nil {
			t.Fatal(err)
		}
		f := &fakeFoundry{target: foundry.Target{Deployment: "test"}, response: string(response)}
		phrase := &models.CustomTravelPhrase{Original: "Ich heiße {name}", SourceLanguage: "de", TargetLanguage: "Danish"}
		_, err = New(f).TranslateCheatsheetIntroduction(t.Context(), "test", phrase)
		if (err == nil) != output.valid {
			t.Fatalf("output %+v: %v", output, err)
		}
		for _, name := range []string{"Daniel", "Therese"} {
			if strings.Contains(string(f.payload), name) {
				t.Fatal("personal names were included in the provider request")
			}
		}
		if !strings.Contains(string(f.payload), "Preserve the exact literal placeholder") {
			t.Fatal("name preservation instruction missing")
		}
	}
}
