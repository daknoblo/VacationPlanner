package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/applog"
	"github.com/daknoblo/vacationplanner/internal/config"
	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/route"
	"github.com/daknoblo/vacationplanner/internal/store"
)

// DemoSnapshot contains real, unmodified UI renders for the offline demo builder.
// Fragments are keyed by their original same-origin GET URL, including queries.
// The builder must remove live scripts, disable writes and rewrite navigation.
type DemoSnapshot struct {
	Pages     map[string][]byte
	Fragments map[string][]byte
	MapData   []byte
	TripID    string
}

// RenderDemo renders synthetic data without configuration discovery, credentials,
// network access, workers, filesystem databases or a listening HTTP server.
// Only the build-time demo command calls this entry point; production routing
// never references it, so the linker discards it from the normal server binary.
// Trip dates are May 12–18 of next year, keeping the dashboard's planned state
// representative without changing the application's real clock.
func RenderDemo(ctx context.Context, language, buildVersion string) (*DemoSnapshot, error) {
	lang, ok := i18n.ParseLang(language)
	if !ok {
		return nil, fmt.Errorf("demo: unsupported language %q", language)
	}
	s, trip, err := newDemoServer(ctx, lang, buildVersion)
	if err != nil {
		return nil, err
	}
	defer s.store.Close()
	ctx = i18n.NewContext(ctx, i18n.NewLocalizer(lang))
	base := "/vacations/" + trip.ID.String()
	out := &DemoSnapshot{
		Pages: make(map[string][]byte), Fragments: make(map[string][]byte), TripID: trip.ID.String(),
	}
	for _, page := range []struct{ file, url string }{
		{"index.html", "/"}, {"vacation.html", base}, {"settings.html", "/settings"},
		{"about.html", "/about"}, {"export.html", base + "/export"},
	} {
		body, err := demoGET(ctx, s, page.url)
		if err != nil {
			return nil, err
		}
		out.Pages[page.file] = body
	}
	out.MapData, err = demoGET(ctx, s, base+"/api/overview-map")
	if err != nil {
		return nil, err
	}

	// Discover nested attachment panels as well as top-level lazy tabs. The
	// bounded, GET-only router below fails closed if templates add a new source.
	pending := make(map[string]bool)
	collect := func(body []byte) {
		for _, url := range demoFragmentURLs(body) {
			pending[url] = true
		}
	}
	for _, body := range out.Pages {
		collect(body)
	}
	for len(pending) > 0 {
		urls := make([]string, 0, len(pending))
		for url := range pending {
			urls = append(urls, url)
		}
		sort.Strings(urls)
		for _, url := range urls {
			delete(pending, url)
			if _, done := out.Fragments[url]; done {
				continue
			}
			if len(out.Fragments) >= 256 {
				return nil, errors.New("demo: too many nested fragments")
			}
			body, err := demoGET(ctx, s, url)
			if err != nil {
				return nil, err
			}
			out.Fragments[url] = body
			collect(body)
		}
	}
	return out, nil
}

func demoFragmentURLs(body []byte) []string {
	getAttr := regexp.MustCompile(`<([a-z][a-z0-9-]*)\b[^>]*\bhx-get="([^"]+)"[^>]*>`)
	var urls []string
	for _, match := range getAttr.FindAllSubmatch(body, -1) {
		// Editing buttons are actions, not lazy subtree placeholders. The
		// static builder disables these controls rather than invoking them.
		if string(match[1]) == "button" || string(match[1]) == "a" {
			continue
		}
		urls = append(urls, html.UnescapeString(string(match[2])))
	}
	return urls
}

func demoGET(ctx context.Context, s *Server, url string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(url) < 1 || url[0] != '/' || (len(url) > 1 && url[1] == '/') {
		return nil, fmt.Errorf("demo: not a same-origin URL: %q", url)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("demo: GET %s returned %d: %s", url, rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("ZgotmplZ")) {
		return nil, fmt.Errorf("demo: unsafe template value at %s", url)
	}
	return bytes.Clone(rec.Body.Bytes()), nil
}

func newDemoServer(ctx context.Context, lang i18n.Lang, buildVersion string) (*Server, *models.Vacation, error) {
	st, err := store.NewSQLite(ctx, ":memory:")
	if err != nil {
		return nil, nil, err
	}
	success := false
	defer func() {
		if !success {
			st.Close()
		}
	}()
	if err := st.Migrate(ctx); err != nil {
		return nil, nil, err
	}
	render, err := newRenderer()
	if err != nil {
		return nil, nil, err
	}
	_, logs := applog.New("demo")
	s := &Server{
		cfg: &config.Config{Env: "demo"},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), logs: logs,
		store: st, render: render, routing: route.New(""),
	}
	// Deliberately do not call New: it creates network-capable providers. The
	// read-only routes below need neither a geocoder nor an image client.
	s.foundry = demoFoundry{}
	s.ai = ai.New(s.foundry)
	trip, err := seedDemo(ctx, st, lang)
	if err != nil {
		return nil, nil, err
	}
	if err := st.PutSetting(ctx, s.foundrySettingKey("chat"), "travel-chat"); err != nil {
		return nil, nil, err
	}
	s.geography = newGeographyWorker(s)
	s.geography.states[trip.ID] = &geographyState{finished: time.Now()}
	// Presentation-only progress for the other planned trip; no queued work or
	// goroutine exists. Main-trip map enrichment remains complete.
	s.geography.states[demoID(2)] = &geographyState{
		status: geographyStatus{Pending: true, Completed: 3, Total: 5},
	}
	r := chi.NewRouter()
	r.Use(s.settingsScope)
	r.Get("/", s.handleIndex)
	r.Get("/settings", s.handleDemoSettings)
	r.Get("/settings/logs", s.handleLogs)
	r.Get("/api/background-status", s.handleBackgroundStatus)
	r.Get("/about", func(w http.ResponseWriter, r *http.Request) {
		s.page(w, r, "about", i18n.FromContext(r.Context()).T("about.title"), aboutView{
			Version: buildVersion, RepoURL: aboutRepoURL, DocsURL: aboutDocsURL,
		})
	})
	r.Route("/vacations/{vacationID}", func(r chi.Router) {
		r.Get("/", s.handleVacationDetail)
		r.Get("/export", s.handleExport)
		r.Get("/cheatsheet", s.handleCheatsheet)
		r.Get("/api/overview-map", s.handleOverviewMap)
		r.Get("/api/daycounts", s.handleDayCounts)
		r.Get("/api/budget", s.handleBudgetFragment)
		r.Get("/api/overview", s.handleOverviewFragment)
		r.Get("/api/ideas", s.handleIdeasFragment)
		r.Get("/api/daycards", s.handleDayCards)
		r.Get("/api/dayroute", s.handleDayRoute)
		r.Get("/api/destination-info", func(w http.ResponseWriter, r *http.Request) {
			// Original fixture prose, not a fetched Wikipedia extract.
			s.fragment(w, r, "destination_info", destinationInfoView{
				Destination: trip.Destination, URL: "https://en.wikipedia.org/wiki/Tuscany",
				Extract: demoText(lang,
					"A week of city walks, shared meals and time to explore Florence, Siena and Rome. All bookings and notes in this itinerary are fictional.",
					"Eine Woche mit Stadtspaziergängen, gemeinsamen Mahlzeiten und Zeit für Florenz, Siena und Rom. Alle Buchungen und Notizen dieser Reise sind erfunden."),
			})
		})
		r.Get("/traveldocs/{kind}/{step}", s.handleTravelDocuments)
	})
	r.Get("/items/{itemID}", s.handleItemRow)
	r.Get("/items/{itemID}/documents", s.handleItemDocuments)
	r.Get("/lodging/{lodgingID}/documents", s.handleLodgingDocuments)
	s.router = r
	success = true
	return s, trip, nil
}

// This view uses the normal settings template and helpers but intentionally
// omits the runtime handler's filesystem inspection of DB files and backups.
func (s *Server) handleDemoSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := i18n.FromContext(ctx)
	settings, err := s.settings(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	foundryView, err := s.foundrySettings(ctx, settings)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	stats, err := s.store.Stats(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	categories, err := s.store.ListCategories(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	people, err := s.store.ListPeople(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vacations, err := s.store.ListVacations(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.page(w, r, "settings", loc.T("page.settings.title"), map[string]any{
		"Languages": i18n.Supported(), "Current": loc.Lang(), "Foundry": foundryView,
		"WeekStart": "monday", "Timezone": "Europe/Rome", "Currency": "€",
		"Currencies": supportedCurrencies, "Timezones": commonTimezones,
		"GeoDefaultBaseURL": geo.DefaultBaseURL, "RouteDefaultBaseURL": route.DefaultBaseURL,
		"LogLevel": s.logs.LevelName(), "LogLevels": applog.Levels(),
		"Categories": categories, "CategoryIcons": defaultCategoryIcons, "People": people,
		"Stats": stats, "DBSize": humanBytes(0), "Backups": []backupView{},
		"AutoVacuum": autoVacuumSetting(settings), "AutoVacuumOptions": autoVacuumOptions,
		"Vacations": vacations,
	})
}

// demoFoundry exposes local illustrative metadata only. No credential
// constructor or provider operation is reachable, even on an accidental call.
type demoFoundry struct{}

func (demoFoundry) Refresh(context.Context) error {
	return errors.New("demo: provider discovery is forbidden")
}

func (demoFoundry) DoChat(context.Context, foundry.Target, []byte) ([]byte, error) {
	return nil, errors.New("demo: model generation is forbidden")
}

func (demoFoundry) ResolveChat(_ context.Context, name string) (foundry.Target, error) {
	if name != "travel-chat" {
		return foundry.Target{}, errors.New("demo: unknown deployment")
	}
	return foundry.Target{Endpoint: "https://demo.invalid/openai/v1", Deployment: name, SupportsTemperature: true}, nil
}

func (demoFoundry) Snapshots(context.Context) ([]foundry.Snapshot, error) {
	return []foundry.Snapshot{{
		Role: "main", Endpoint: "https://demo.invalid/openai/v1",
		ResourceID: "/subscriptions/00000000-0000-4000-8000-000000000000/resourceGroups/demo/providers/Microsoft.CognitiveServices/accounts/vacation-demo",
		UpdatedAt:  time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Deployments: []foundry.Deployment{{
			Name: "travel-chat", Model: "gpt-4o", ModelVersion: "2024-08-06",
			ModelFormat: "OpenAI", ProvisioningState: "Succeeded", ChatSupported: true,
		}},
	}}, nil
}
