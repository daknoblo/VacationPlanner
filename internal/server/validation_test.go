package server

import "testing"

func TestClampRange(t *testing.T) {
	tests := []struct {
		name               string
		start, end, minLen int
		wantStart, wantEnd int
	}{
		{"regular", 540, 600, 30, 540, 600},
		{"end before start gets the minimum", 600, 540, 30, 600, 630},
		{"midnight start stays inside the day", 1440, 1440, 30, 1410, 1440},
		{"end is capped at midnight", 1430, 1500, 30, 1410, 1440},
		{"negative input", -60, -10, 30, 0, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end := clampRange(tc.start, tc.end, tc.minLen)
			if start != tc.wantStart || end != tc.wantEnd {
				t.Fatalf("clampRange(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tc.start, tc.end, tc.minLen, start, end, tc.wantStart, tc.wantEnd)
			}
			if start < 0 || end > dayMinutes || end <= start {
				t.Fatalf("result out of range: start=%d end=%d", start, end)
			}
		})
	}
}

func TestValidBaseURL(t *testing.T) {
	valid := []string{
		"https://api.openai.com/v1",
		"http://localhost:11434/v1",   // Ollama
		"http://127.0.0.1:8080",       // local service
		"http://192.168.1.10:3000/v1", // self-hosted on the LAN
		"https://photon.komoot.io",
	}
	for _, raw := range valid {
		if !validBaseURL(raw) {
			t.Errorf("validBaseURL(%q) = false, want true", raw)
		}
	}

	invalid := []string{
		"",
		"not a url",
		"ftp://example.com",
		"file:///etc/passwd",
		"https://",
		"http://169.254.169.254/latest/meta-data/", // cloud instance metadata
		"http://[fe80::1]:8080",                    // IPv6 link-local
		"http://0.0.0.0:8080",
		"http://224.0.0.1",
	}
	for _, raw := range invalid {
		if validBaseURL(raw) {
			t.Errorf("validBaseURL(%q) = true, want false", raw)
		}
	}
}
