package currency

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const Endpoint = "https://api.frankfurter.dev/v2/rates?base=EUR"

type Rate struct {
	Date  string  `json:"date"`
	Base  string  `json:"base"`
	Quote string  `json:"quote"`
	Value float64 `json:"rate"`
}

type Snapshot struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Rates     map[string]Rate `json:"rates"`
}

type Client struct {
	http     *http.Client
	endpoint string
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 10 * time.Second}, endpoint: Endpoint}
}

func ValidCode(code string) bool {
	return len(code) == 3 && strings.IndexFunc(code, func(letter rune) bool { return letter < 'A' || letter > 'Z' }) == -1
}

func (client *Client) Fetch(ctx context.Context) (Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.endpoint, nil)
	if err != nil {
		return Snapshot{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("currency: provider status %d", response.StatusCode)
	}
	var rates []Rate
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&rates); err != nil {
		return Snapshot{}, fmt.Errorf("currency: invalid rates: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Snapshot{}, fmt.Errorf("currency: unexpected response content")
	}
	if len(rates) == 0 || len(rates) > 300 {
		return Snapshot{}, fmt.Errorf("currency: invalid number of rates")
	}
	snapshot := Snapshot{FetchedAt: time.Now().UTC(), Rates: make(map[string]Rate)}
	for _, rate := range rates {
		date, err := time.Parse("2006-01-02", rate.Date)
		if err != nil || date.After(snapshot.FetchedAt.Add(24*time.Hour)) || rate.Base != "EUR" ||
			!ValidCode(rate.Quote) || rate.Value < 1e-9 || rate.Value > 1e9 || math.IsNaN(rate.Value) || math.IsInf(rate.Value, 0) {
			return Snapshot{}, fmt.Errorf("currency: invalid rate metadata")
		}
		if _, duplicate := snapshot.Rates[rate.Quote]; duplicate {
			return Snapshot{}, fmt.Errorf("currency: duplicate currency")
		}
		snapshot.Rates[rate.Quote] = rate
	}
	return snapshot, nil
}

type Shortcut struct {
	Multiply bool
	Factor   float64
	Error    float64
}

func MentalRule(rate float64) Shortcut {
	if rate < 1e-9 || rate > 1e9 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return Shortcut{}
	}
	multiply := rate < 1
	target := rate
	if multiply {
		target = 1 / rate
	}
	scale := math.Pow(10, math.Floor(math.Log10(target)))
	factor := math.Round(target/scale*10) * scale / 10
	best := math.Inf(1)
	for _, simple := range []float64{1, 1.5, 2, 2.5, 3, 3.5, 4, 5, 6, 7, 7.5, 8, 9, 10} {
		candidate := simple * scale
		errorRatio := math.Abs(rate/candidate - 1)
		if multiply {
			errorRatio = math.Abs(candidate*rate - 1)
		}
		if errorRatio <= 0.05 && errorRatio < best {
			factor, best = candidate, errorRatio
		}
	}
	errorRatio := math.Abs(rate/factor - 1)
	if multiply {
		errorRatio = math.Abs(factor*rate - 1)
	}
	return Shortcut{Multiply: multiply, Factor: factor, Error: errorRatio}
}
