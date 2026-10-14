package currency

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMentalRule(t *testing.T) {
	for _, test := range []struct {
		rate     float64
		factor   float64
		multiply bool
	}{
		{5.01, 5, false}, {7.46, 7.5, false}, {38.2, 40, false},
		{0.84, 1.2, true}, {1, 1, false}, {16342, 16000, false},
	} {
		got := MentalRule(test.rate)
		if got.Factor != test.factor || got.Multiply != test.multiply || got.Error > 0.05 {
			t.Fatalf("rate %v: %+v", test.rate, got)
		}
	}
	for _, rate := range []float64{0, -1} {
		if got := MentalRule(rate); got.Factor != 0 {
			t.Fatal("invalid rate produced a rule", got)
		}
	}
}

func TestFetchValidatesDailyEuroRates(t *testing.T) {
	for _, test := range []struct {
		body string
		ok   bool
	}{
		{`[{"date":"2026-01-02","base":"EUR","quote":"DKK","rate":7.46}]`, true},
		{`[]`, false},
		{`[{"date":"2026-01-02","base":"USD","quote":"DKK","rate":7.46}]`, false},
		{`[{"date":"2026-01-02","base":"EUR","quote":"DKK","rate":0}]`, false},
		{`[{"date":"invalid","base":"EUR","quote":"DKK","rate":7.46}]`, false},
		{`[{"date":"2026-01-02","base":"EUR","quote":"<b>","rate":7.46}]`, false},
		{`[{"date":"2026-01-02","base":"EUR","quote":"DKK","rate":7.46},{"date":"2026-01-02","base":"EUR","quote":"DKK","rate":7.46}]`, false},
		{`[{"date":"2026-01-02","base":"EUR","quote":"DKK","rate":7.46}] {}`, false},
	} {
		t.Run(test.body, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodGet || request.URL.RawQuery != "base=EUR" {
					t.Error("unexpected provider request")
				}
				_, _ = response.Write([]byte(test.body))
			}))
			defer provider.Close()
			client := New()
			client.endpoint = provider.URL + "?base=EUR"
			got, err := client.Fetch(t.Context())
			if (err == nil) != test.ok {
				t.Fatal(got, err)
			}
			if test.ok && (got.Rates["DKK"].Value != 7.46 || got.FetchedAt.IsZero()) {
				t.Fatal("rate or timestamp missing", got)
			}
		})
	}
}
