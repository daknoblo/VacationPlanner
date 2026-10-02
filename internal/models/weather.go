package models

import "time"

// WeatherSample describes a three-hour forecast interval ending at Time.
type WeatherSample struct {
	Time        time.Time `json:"time"`
	Temperature float64   `json:"temperature"`
	Code        int       `json:"code"`
	RainChance  float64   `json:"rain_chance"`
	RainMM      float64   `json:"rain_mm"`
	SnowMM      float64   `json:"snow_mm"`
	WindMS      float64   `json:"wind_ms"`
}

// WeatherCache is shared only by identical coordinates and provider version.
type WeatherCache struct {
	Key         string
	Latitude    float64
	Longitude   float64
	Status      string
	Attempt     string
	AttemptedAt time.Time
	UpdatedAt   time.Time
	ErrorCode   string
	Samples     []WeatherSample
}
