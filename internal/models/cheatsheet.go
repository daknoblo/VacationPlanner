package models

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type TravelPhrase struct {
	Key           string `json:"key"`
	Text          string `json:"text"`
	Pronunciation string `json:"pronunciation"`
}

type Cheatsheet struct {
	VacationID     uuid.UUID      `json:"-"`
	SourceLanguage string         `json:"-"`
	DestinationKey string         `json:"-"`
	CreatedAt      time.Time      `json:"-"`
	Country        string         `json:"country"`
	Language       string         `json:"language"`
	Phrases        []TravelPhrase `json:"phrases"`
}

type PhraseMeaning struct {
	Key     string `json:"key"`
	English string `json:"meaning"`
}

// TravelPhraseMeanings fixes the small vocabulary independently of model output.
func TravelPhraseMeanings() []PhraseMeaning {
	return []PhraseMeaning{
		{"hello", "Hello"}, {"good_day", "Good day"}, {"goodbye", "Goodbye"},
		{"please", "Please"}, {"thank_you", "Thank you"}, {"yes", "Yes"}, {"no", "No"},
		{"excuse_me", "Excuse me"}, {"sorry", "I am sorry"}, {"dont_understand", "I do not understand"},
		{"english", "Do you speak English?"}, {"help", "Help!"}, {"doctor", "I need a doctor"},
		{"toilet", "Where is the toilet?"}, {"water", "Water"}, {"menu", "The menu, please"},
		{"vegetarian", "I eat vegetarian food"}, {"bill", "The bill, please"},
		{"how_much", "How much does it cost?"}, {"card", "Can I pay by card?"},
		{"ticket", "A ticket, please"}, {"hotel", "Where is the hotel?"},
	}
}

// Validate requires one usable translation for every fixed phrase.
func (c *Cheatsheet) Validate() error {
	if strings.TrimSpace(c.Country) == "" || strings.TrimSpace(c.Language) == "" ||
		len(c.Country) > 150 || len(c.Language) > 150 || len(c.Phrases) != len(TravelPhraseMeanings()) {
		return errors.New("cheatsheet has incomplete country, language or phrase metadata")
	}
	allowed := make(map[string]bool)
	for _, meaning := range TravelPhraseMeanings() {
		allowed[meaning.Key] = true
	}
	for _, phrase := range c.Phrases {
		if !allowed[phrase.Key] || strings.TrimSpace(phrase.Text) == "" || len(phrase.Text) > 500 || len(phrase.Pronunciation) > 500 {
			return errors.New("cheatsheet contains a missing, duplicate or invalid phrase")
		}
		delete(allowed, phrase.Key)
	}
	return nil
}
