package weather

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const forecastFixture = `{"list":[{"dt":1790928000,"main":{"temp":18.5},"weather":[{"id":500}],
	"pop":0.4,"wind":{"speed":3.1},"rain":{"3h":1.2}},{"dt":1790938800,"main":{"temp":20},
	"weather":[{"id":800}],"pop":0,"wind":{"speed":2}}]}`

func TestForecastValidationAndSecretRedaction(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
		want       error
	}{
		{"valid", forecastFixture, 200, nil},
		{"key", "private-key", 401, ErrAuth},
		{"quota", "", 429, ErrQuota},
		{"server", "private-key", 500, ErrRequest},
		{"redirect", "", 302, ErrRequest},
		{"malformed", "{", 200, ErrData},
		{"empty", `{"list":[]}`, 200, ErrData},
		{"missing temperature", strings.Replace(forecastFixture, `"temp":18.5`, `"other":18.5`, 1), 200, ErrData},
		{"missing probability", strings.Replace(forecastFixture, `"pop":0.4`, `"other":0.4`, 1), 200, ErrData},
		{"invalid probability", strings.Replace(forecastFixture, `"pop":0.4`, `"pop":1.1`, 1), 200, ErrData},
		{"negative rain", strings.Replace(forecastFixture, `"3h":1.2`, `"3h":-1`, 1), 200, ErrData},
		{"duplicate timestamp", strings.Replace(forecastFixture, "1790938800", "1790928000", 1), 200, ErrData},
		{"gap", strings.Replace(forecastFixture, "1790938800", "1790971200", 1), 200, ErrData},
		{"too large", strings.Repeat(" ", (1<<20)+1), 200, ErrData},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := New("private-key")
			c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.openweathermap.org" || r.URL.Path != "/data/2.5/forecast" ||
					r.URL.Query().Get("appid") != "private-key" || r.URL.Query().Get("units") != "metric" ||
					r.URL.Query().Get("lat") != "0" || r.URL.Query().Get("lon") != "-10" {
					t.Fatal("wrong provider endpoint or query")
				}
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})
			samples, err := c.Forecast(t.Context(), 0, -10)
			if !errors.Is(err, tt.want) {
				t.Fatal(err, tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "private-key") {
				t.Fatal("secret disclosed")
			}
			if err == nil && (len(samples) != 2 || samples[0].RainMM != 1.2 || samples[0].Temperature != 18.5) {
				t.Fatal(samples)
			}
		})
	}
	c := New("private-key")
	c.http.Transport = transportFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("URL contains private-key")
	})
	if _, err := c.Forecast(t.Context(), 0, 0); !errors.Is(err, ErrRequest) {
		t.Fatal("transport error must be sanitized", err)
	}
	if _, err := c.Forecast(t.Context(), 91, 0); !errors.Is(err, ErrRequest) {
		t.Fatal("invalid latitude accepted", err)
	}
	if New("").Enabled() || Key(0, 1) == Key(1, 0) || Key(1.0000001, 0) == Key(1.0000002, 0) {
		t.Fatal("configuration or cache key broken")
	}
}
