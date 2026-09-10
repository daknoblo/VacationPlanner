package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/i18n"
)

func TestCostRejectsNonFiniteValues(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf", "Infinity"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(url.Values{"cost": {value}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := parseCostPtr(r, i18n.NewLocalizer(i18n.LangEN)); err == nil {
			t.Errorf("non-finite amount accepted: %s", value)
		}
		s := newIntegrationServer(t)
		form := url.Values{"title": {"Trip"}, "destination": {"Paris"}, "start_date": {"2026-09-01"}, "end_date": {"2026-09-02"}, "budget": {value}}
		if rec := postAISettings(s, "/vacations", form, true); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("non-finite trip budget accepted: %s (status %d)", value, rec.Code)
		}
	}
}
