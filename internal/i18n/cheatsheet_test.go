package i18n

import (
	"testing"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestCheatsheetMeaningsAreTranslated(t *testing.T) {
	for _, lang := range Supported() {
		loc := NewLocalizer(lang)
		for _, meaning := range models.TravelPhraseMeanings() {
			key := "cheatsheet.phrase." + meaning.Key
			if got := loc.T(key); got == key || got == "" {
				t.Errorf("missing phrase meaning %s for %s", key, lang)
			}
		}
	}
}
