package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	textcurrency "golang.org/x/text/currency"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"

	"github.com/daknoblo/vacationplanner/internal/currency"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

const settingCurrencyRates = "currency.rates"
const settingCurrencyAttempt = "currency.last_attempt"
const settingCurrencyError = "currency.error"

type currencyProvider interface {
	Fetch(context.Context) (currency.Snapshot, error)
}

type currencyView struct {
	Code      string
	Codes     []string
	Labels    map[string]string
	Rate      float64
	Date      string
	Available bool
	Same      bool
	Stale     bool
	Failed    bool
	Loading   bool
	Factor    float64
	Multiply  bool
	Error     float64
	RateLabel string
	Rule      string
	RuleError string
}

func currencyTripKey(id uuid.UUID) string { return "currency.trip." + id.String() }

func currencySnapshot(settings map[string]string) currency.Snapshot {
	var snapshot currency.Snapshot
	_ = json.Unmarshal([]byte(settings[settingCurrencyRates]), &snapshot)
	return snapshot
}

func (s *Server) currencyView(ctx context.Context, id uuid.UUID) (currencyView, error) {
	settings, err := s.settings(ctx)
	if err != nil {
		return currencyView{}, err
	}
	snapshot := currencySnapshot(settings)
	view := currencyView{Code: settings[currencyTripKey(id)], Failed: settings[settingCurrencyError] != "",
		Loading: snapshot.FetchedAt.IsZero() && settings[settingCurrencyError] == "", Labels: make(map[string]string)}
	unique := map[string]bool{"EUR": true}
	countries := make(map[string]string)
	labels := make(map[string][]string)
	english, german := display.Regions(language.English), display.Regions(language.German)
	iterator := textcurrency.Query(textcurrency.Date(time.Now()))
	for iterator.Next() {
		code := iterator.Unit().String()
		unique[code] = true
		for _, name := range []string{english.Name(iterator.Region()), german.Name(iterator.Region())} {
			key := strings.ToLower(name)
			if previous, exists := countries[key]; !exists {
				countries[key] = code
			} else if previous != code {
				countries[key] = ""
			}
		}
		name := english.Name(iterator.Region())
		if i18n.FromContext(ctx).Code() == "de" {
			name = german.Name(iterator.Region())
		}
		labels[code] = append(labels[code], name)
	}
	for code := range snapshot.Rates {
		if currency.ValidCode(code) {
			unique[code] = true
		}
	}
	for code := range unique {
		view.Codes = append(view.Codes, code)
		view.Labels[code] = code
		if names := labels[code]; len(names) > 0 {
			sort.Strings(names)
			view.Labels[code] += " · " + strings.Join(names[:min(3, len(names))], ", ")
			if len(names) > 3 {
				view.Labels[code] += " ..."
			}
		}
	}
	sort.Strings(view.Codes)
	if view.Code == "" {
		vacation, err := s.store.GetVacation(ctx, id)
		if err != nil {
			return view, err
		}
		parts := strings.Split(vacation.Destination, ",")
		view.Code = countries[strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))]
		if view.Code == "" {
			key, _ := models.CheatsheetDestinationKey(vacation)
			for _, locale := range []string{"en", "de"} {
				sheet, err := s.store.GetCheatsheet(ctx, id, locale)
				if err == nil && sheet.DestinationKey == key {
					view.Code = countries[strings.ToLower(strings.TrimSpace(sheet.Country))]
					if view.Code != "" {
						break
					}
				}
			}
		}
		if view.Code != "EUR" {
			rate, available := snapshot.Rates[view.Code]
			date, dateErr := time.Parse("2006-01-02", rate.Date)
			if !available || dateErr != nil || date.Before(time.Now().UTC().AddDate(0, 0, -7)) {
				view.Code = ""
			}
		}
	}
	if view.Code == "EUR" {
		view.Available, view.Same, view.Rate, view.Factor = true, true, 1, 1
		view.Loading, view.Failed = false, false
		return view, nil
	}
	if rate, ok := snapshot.Rates[view.Code]; ok && rate.Value > 0 && rate.Value < 1e9 {
		date, err := time.Parse("2006-01-02", rate.Date)
		if err == nil {
			view.Rate, view.Available, view.Date = rate.Value, true, fmtDate(date)
			view.Stale = date.Before(time.Now().UTC().AddDate(0, 0, -7))
			rule := currency.MentalRule(rate.Value)
			view.Factor, view.Multiply, view.Error = rule.Factor, rule.Multiply, rule.Error*100
			loc := i18n.FromContext(ctx)
			format := func(value float64) string {
				text := strconv.FormatFloat(value, 'g', 6, 64)
				if loc.Code() == "de" {
					text = strings.ReplaceAll(text, ".", ",")
				}
				return text
			}
			view.RateLabel = "1 EUR = " + format(rate.Value) + " " + view.Code
			key := "currency.rule.divide"
			if rule.Multiply {
				key = "currency.rule.multiply"
			}
			view.Rule = loc.T(key, view.Code, format(rule.Factor))
			view.RuleError = loc.T("currency.rule.error", format(float64(int(view.Error*10+0.5))/10))
		}
	}
	return view, nil
}

func (s *Server) handleCurrency(w http.ResponseWriter, request *http.Request) {
	id, err := urlUUID(request, "vacationID")
	if err != nil {
		s.notFound(w, request)
		return
	}
	if _, err := s.store.GetVacation(request.Context(), id); err != nil {
		if isNotFound(err) {
			s.notFound(w, request)
		} else {
			s.serverError(w, request, err)
		}
		return
	}
	view, err := s.currencyView(request.Context(), id)
	if err != nil {
		s.serverError(w, request, err)
		return
	}
	if request.Method == http.MethodPost {
		code := strings.TrimSpace(formStr(request, "currency"))
		if !currency.ValidCode(code) {
			http.Error(w, i18n.FromContext(request.Context()).T("currency.invalid"), http.StatusBadRequest)
			return
		}
		index := sort.SearchStrings(view.Codes, code)
		if index == len(view.Codes) || view.Codes[index] != code {
			http.Error(w, i18n.FromContext(request.Context()).T("currency.invalid"), http.StatusBadRequest)
			return
		}
		if err := s.putSetting(request.Context(), currencyTripKey(id), code); err != nil {
			s.serverError(w, request, err)
			return
		}
		view, err = s.currencyView(request.Context(), id)
		if err != nil {
			s.serverError(w, request, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.fragment(w, request, "currency_values", view)
}

func (s *Server) refreshCurrency(ctx context.Context, now time.Time) error {
	if s.currency == nil {
		return nil
	}
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return err
	}
	if snapshot := currencySnapshot(settings); !snapshot.FetchedAt.IsZero() && now.Sub(snapshot.FetchedAt) < 6*time.Hour {
		return nil
	}
	if attempted, err := time.Parse(time.RFC3339, settings[settingCurrencyAttempt]); err == nil && now.Sub(attempted) < 30*time.Minute {
		return nil
	}
	if err := s.store.PutSetting(ctx, settingCurrencyAttempt, now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	s.currencyActive.Store(true)
	defer s.currencyActive.Store(false)
	snapshot, err := s.currency.Fetch(ctx)
	if err != nil {
		if ctx.Err() == nil {
			_ = s.store.PutSetting(ctx, settingCurrencyError, "1")
		}
		return err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if err := s.store.PutSetting(ctx, settingCurrencyRates, string(encoded)); err != nil {
		return err
	}
	return s.store.PutSetting(ctx, settingCurrencyError, "")
}

func (s *Server) StartCurrencyWorker(parent context.Context) func() {
	s.currencyStart.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		done := make(chan struct{})
		s.currencyStop = func() { cancel(); <-done }
		go func() {
			defer close(done)
			timer := time.NewTimer(0)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
				requestCtx, cancelRequest := context.WithTimeout(ctx, 12*time.Second)
				err := s.refreshCurrency(requestCtx, time.Now())
				cancelRequest()
				if err != nil && ctx.Err() == nil {
					s.log.Warn("background currency refresh failed", "err", err)
				}
				timer.Reset(30 * time.Minute)
			}
		}()
	})
	return s.currencyStop
}
