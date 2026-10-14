package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/currency"
	"github.com/daknoblo/vacationplanner/internal/i18n"
)

type testCurrency struct {
	calls int
	fail  bool
}

func (provider *testCurrency) Fetch(context.Context) (currency.Snapshot, error) {
	provider.calls++
	if provider.fail {
		return currency.Snapshot{}, errors.New("provider unavailable")
	}
	now := time.Now().UTC()
	return currency.Snapshot{FetchedAt: now, Rates: map[string]currency.Rate{
		"DKK": {Date: now.Format("2006-01-02"), Base: "EUR", Quote: "DKK", Value: 7.46},
	}}, nil
}

func TestCurrencyReadsAndSelectionNeverFetchProvider(t *testing.T) {
	server := newIntegrationServer(t)
	provider := &testCurrency{}
	server.currency = provider
	vacation := sampleVacation()
	if err := server.store.CreateVacation(t.Context(), vacation); err != nil {
		t.Fatal(err)
	}
	path := "/vacations/" + vacation.ID.String() + "/currency"
	if response := postAISettings(server, path, url.Values{"currency": {"DKK"}}, false); response.Code != http.StatusForbidden {
		t.Fatal("currency selection must require CSRF", response.Code)
	}
	if response := postAISettings(server, path, url.Values{"currency": {"DKK"}}, true); response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	if response := postAISettings(server, path, url.Values{"currency": {"<b>"}}, true); response.Code != http.StatusBadRequest {
		t.Fatal("invalid currency accepted", response.Code)
	}
	for _, suffix := range []string{"/currency", ""} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/vacations/"+vacation.ID.String()+suffix, nil))
		if response.Code != http.StatusOK {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	if provider.calls != 0 {
		t.Fatal("page read or currency selection started provider work")
	}
	if err := server.refreshCurrency(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, language := range []i18n.Lang{i18n.LangEN, i18n.LangDE} {
		ctx := i18n.NewContext(t.Context(), i18n.NewLocalizer(language))
		view, err := server.currencyView(ctx, vacation.ID)
		if err != nil || !view.Available || view.Rate != 7.46 || view.Factor != 7.5 || view.Multiply || view.Code != "DKK" {
			t.Fatal(view, err)
		}
		if language == i18n.LangDE && (!strings.Contains(view.Rule, "durch 7,5") || !strings.Contains(view.RateLabel, "7,46")) {
			t.Fatal("German decimal formatting or rule is incorrect", view)
		}
	}
	if provider.calls != 1 {
		t.Fatal("reading cached rates repeated the provider call")
	}
	if response := postAISettings(server, path, url.Values{"currency": {"EUR"}}, true); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "1 EUR = 1 EUR") {
		t.Fatal("Euro-to-Euro must not invent a foreign rate", response.Code, response.Body.String())
	}
}

func TestCurrencyRefreshRetainsRatesAndPacesFailures(t *testing.T) {
	server := newIntegrationServer(t)
	provider := &testCurrency{}
	server.currency = provider
	now := time.Now().UTC()
	if err := server.refreshCurrency(t.Context(), now); err != nil {
		t.Fatal(err)
	}
	if err := server.refreshCurrency(t.Context(), now.Add(time.Hour)); err != nil || provider.calls != 1 {
		t.Fatal("fresh rates were requested again", err, provider.calls)
	}
	provider.fail = true
	if err := server.refreshCurrency(t.Context(), now.Add(7*time.Hour)); err == nil || provider.calls != 2 {
		t.Fatal("failed refresh was not detected", err, provider.calls)
	}
	settings, err := server.store.GetSettings(t.Context())
	if err != nil || currencySnapshot(settings).Rates["DKK"].Value != 7.46 || settings[settingCurrencyError] == "" {
		t.Fatal("failed refresh discarded cached values or hid its state", err)
	}
	restarted := &Server{store: server.store, currency: provider}
	if err := restarted.refreshCurrency(t.Context(), now.Add(7*time.Hour+time.Minute)); err != nil || provider.calls != 2 {
		t.Fatal("restart retried a recent failed request", err, provider.calls)
	}
}

func TestCurrencyUsesUnambiguousSavedCountry(t *testing.T) {
	server := newIntegrationServer(t)
	server.currency = &testCurrency{}
	if err := server.refreshCurrency(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	vacation := sampleVacation()
	vacation.Destination = "Kopenhagen, Dänemark"
	if err := server.store.CreateVacation(t.Context(), vacation); err != nil {
		t.Fatal(err)
	}
	view, err := server.currencyView(t.Context(), vacation.ID)
	if err != nil || view.Code != "DKK" || !strings.Contains(view.Labels["DKK"], "Denmark") {
		t.Fatal("saved country did not resolve its currency", view.Code, err)
	}
	if err := server.putSetting(t.Context(), currencyTripKey(vacation.ID), "EUR"); err != nil {
		t.Fatal(err)
	}
	view, err = server.currencyView(t.Context(), vacation.ID)
	if err != nil || view.Code != "EUR" || !view.Same {
		t.Fatal("manual selection must override country inference", view, err)
	}
}

type blockingCurrency struct{ started chan struct{} }

func (provider *blockingCurrency) Fetch(ctx context.Context) (currency.Snapshot, error) {
	close(provider.started)
	<-ctx.Done()
	return currency.Snapshot{}, ctx.Err()
}

func TestCurrencyWorkerNeverBlocksTripReads(t *testing.T) {
	server := newIntegrationServer(t)
	vacation := sampleVacation()
	if err := server.store.CreateVacation(t.Context(), vacation); err != nil {
		t.Fatal(err)
	}
	provider := &blockingCurrency{started: make(chan struct{})}
	server.currency = provider
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := server.StartCurrencyWorker(ctx)
	defer stop()
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("currency worker did not start")
	}
	for _, suffix := range []string{"", "/currency"} {
		readCtx, cancelRead := context.WithTimeout(t.Context(), time.Second)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequestWithContext(readCtx, http.MethodGet, "/vacations/"+vacation.ID.String()+suffix, nil))
		cancelRead()
		if response.Code != http.StatusOK {
			t.Fatal("currency provider blocked the page", response.Code)
		}
	}
	status, err := server.backgroundStatus(t.Context())
	if err != nil || !status.Active || status.Determinate {
		t.Fatal("currency work is not shown as active", status, err)
	}
	cancel()
	stop()
}
