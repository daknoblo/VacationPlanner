package server

import "testing"

func TestValidAutoVacuum(t *testing.T) {
	for _, v := range []string{"off", "daily", "3days", "weekly", "biweekly", "monthly"} {
		if !validAutoVacuum(v) {
			t.Errorf("validAutoVacuum(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "yearly", "hourly", "on"} {
		if validAutoVacuum(v) {
			t.Errorf("validAutoVacuum(%q) = true, want false", v)
		}
	}
	// Every selectable option except "off" must map to a duration.
	for _, v := range autoVacuumOptions {
		if v == "off" {
			continue
		}
		if _, ok := autoVacuumIntervals[v]; !ok {
			t.Errorf("option %q has no interval duration", v)
		}
	}
}

func TestAutoVacuumSetting(t *testing.T) {
	if got := autoVacuumSetting(map[string]string{}); got != "off" {
		t.Errorf("empty settings = %q, want off", got)
	}
	if got := autoVacuumSetting(map[string]string{settingAutoVacuum: "weekly"}); got != "weekly" {
		t.Errorf("= %q, want weekly", got)
	}
	if got := autoVacuumSetting(map[string]string{settingAutoVacuum: "bogus"}); got != "off" {
		t.Errorf("invalid = %q, want off", got)
	}
}
