package models

import (
	"strings"
	"unicode"

	"github.com/google/uuid"
)

type IdeaDescription struct {
	ItemID                                 uuid.UUID
	Title, Category, Location, Destination string
	Attempt, Status, English, German       string
}

// ShortDescription keeps existing prose to one short, Unicode-safe sentence.
func ShortDescription(value string) string {
	runes := []rune(strings.Join(strings.Fields(value), " "))
	for i, r := range runes {
		if strings.ContainsRune(".!?", r) && (i+1 == len(runes) || unicode.IsSpace(runes[i+1])) {
			runes = runes[:i+1]
			break
		}
	}
	if len(runes) <= 160 {
		return string(runes)
	}
	end := 159
	for i := 158; i >= 120; i-- {
		if unicode.IsSpace(runes[i]) {
			end = i
			break
		}
	}
	return string(runes[:end]) + "\u2026"
}
