package models

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// CheatsheetDestinationKey includes coordinates because identical place names can
// refer to destinations with different local languages.
func CheatsheetDestinationKey(v *Vacation) (string, error) {
	data, err := json.Marshal(struct {
		Destination         string
		Latitude, Longitude *float64
		Version             int
	}{v.Destination, v.Latitude, v.Longitude, 1})
	if err != nil {
		return "", fmt.Errorf("encoding cheatsheet destination: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

type CheatsheetJob struct {
	VacationID     uuid.UUID
	SourceLanguage string
	DestinationKey string
	Key            string
	Status         string
	Attempt        string
}

// CustomTravelPhrase is cached independently of the built-in vocabulary.
type CustomTravelPhrase struct {
	VacationID     uuid.UUID `json:"-"`
	SourceLanguage string    `json:"-"`
	DestinationKey string    `json:"-"`
	TargetLanguage string    `json:"-"`
	Original       string    `json:"-"`
	Text           string    `json:"text"`
	Pronunciation  string    `json:"pronunciation"`
}

func NormalizeCustomPhrase(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 500 {
		return "", errors.New("phrase must contain between 1 and 500 characters")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return "", errors.New("phrase contains unsupported control characters")
		}
	}
	return text, nil
}

func (p *CustomTravelPhrase) JobKey() string {
	return fmt.Sprintf("phrase:%x", sha256.Sum256([]byte(p.TargetLanguage+"\x00"+p.Original)))
}

func (p *CustomTravelPhrase) Validate() error {
	if _, err := NormalizeCustomPhrase(p.Original); err != nil {
		return err
	}
	if strings.TrimSpace(p.Text) == "" || strings.TrimSpace(p.Pronunciation) == "" ||
		!utf8.ValidString(p.Text) || !utf8.ValidString(p.Pronunciation) ||
		utf8.RuneCountInString(p.Text) > 2000 || utf8.RuneCountInString(p.Pronunciation) > 2000 {
		return errors.New("phrase translation or pronunciation is missing or too long")
	}
	return nil
}

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
