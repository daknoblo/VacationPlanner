// Package weather reads the free OpenWeatherMap five-day forecast.
package weather

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

const endpoint = "https://api.openweathermap.org/data/2.5/forecast"

var (
	ErrAuth    = errors.New("weather: API key not accepted")
	ErrQuota   = errors.New("weather: provider quota exceeded")
	ErrRequest = errors.New("weather: provider request failed")
	ErrData    = errors.New("weather: invalid provider data")
)

type Client struct {
	key  string
	http *http.Client
}

func New(key string) *Client {
	return &Client{key: key, http: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *Client) Enabled() bool { return c != nil && c.key != "" }

func ValidCoordinates(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) && !math.IsInf(lat, 0) && !math.IsInf(lng, 0) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

func Key(lat, lng float64) string {
	return fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "openweather-2.5:%g:%g", lat, lng)))
}

func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrAuth):
		return "auth"
	case errors.Is(err, ErrQuota):
		return "quota"
	case errors.Is(err, ErrData):
		return "data"
	default:
		return "request"
	}
}

func (c *Client) Forecast(ctx context.Context, lat, lng float64) ([]models.WeatherSample, error) {
	if !c.Enabled() || !ValidCoordinates(lat, lng) {
		return nil, ErrRequest
	}
	q := url.Values{"appid": {c.key}, "lat": {fmt.Sprint(lat)}, "lon": {fmt.Sprint(lng)}, "units": {"metric"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrRequest
	}
	req.Header.Set("User-Agent", "VacationPlanner/Weather")
	resp, err := c.http.Do(req)
	if err != nil {
		// Transport errors can contain the full URL, including the API key.
		return nil, ErrRequest
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrAuth
	case http.StatusTooManyRequests:
		return nil, ErrQuota
	case http.StatusOK:
	default:
		return nil, ErrRequest
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, ErrData
	}
	var reply struct {
		List []struct {
			DT   int64 `json:"dt"`
			Main struct {
				Temp *float64 `json:"temp"`
			} `json:"main"`
			Weather []struct {
				ID int `json:"id"`
			} `json:"weather"`
			Pop  *float64 `json:"pop"`
			Wind struct {
				Speed *float64 `json:"speed"`
			} `json:"wind"`
			Rain struct {
				MM float64 `json:"3h"`
			} `json:"rain"`
			Snow struct {
				MM float64 `json:"3h"`
			} `json:"snow"`
		} `json:"list"`
	}
	if err := json.Unmarshal(body, &reply); err != nil || len(reply.List) == 0 || len(reply.List) > 40 {
		return nil, ErrData
	}
	samples := make([]models.WeatherSample, 0, len(reply.List))
	var previous int64
	for _, p := range reply.List {
		if p.DT <= previous || p.Main.Temp == nil || *p.Main.Temp < -100 || *p.Main.Temp > 70 ||
			p.Pop == nil || *p.Pop < 0 || *p.Pop > 1 || p.Wind.Speed == nil || *p.Wind.Speed < 0 ||
			*p.Wind.Speed > 150 || p.Rain.MM < 0 || p.Snow.MM < 0 || len(p.Weather) == 0 ||
			p.Weather[0].ID < 200 || p.Weather[0].ID > 804 {
			return nil, ErrData
		}
		if previous > 0 && p.DT-previous != 3*60*60 {
			return nil, ErrData
		}
		previous = p.DT
		samples = append(samples, models.WeatherSample{Time: time.Unix(p.DT, 0).UTC(),
			Temperature: *p.Main.Temp, Code: p.Weather[0].ID, RainChance: *p.Pop,
			RainMM: p.Rain.MM, SnowMM: p.Snow.MM, WindMS: *p.Wind.Speed})
	}
	return samples, nil
}
