package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestDemoSnapshots(t *testing.T) {
	t.Parallel()
	for _, language := range []string{"en", "de"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			snapshot, err := RenderDemo(t.Context(), language, "demo-test-version")
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.TripID != demoID(1).String() || len(snapshot.Pages) != 5 {
				t.Fatalf("unexpected snapshot: trip %s, %d pages", snapshot.TripID, len(snapshot.Pages))
			}
			base := "/vacations/" + snapshot.TripID
			for _, name := range []string{"index", "vacation", "settings", "about", "export"} {
				body := snapshot.Pages[name+".html"]
				for _, want := range []string{"<!DOCTYPE html>", `lang="` + language + `"`} {
					if !bytes.Contains(body, []byte(want)) {
						t.Errorf("%s missing %q", name, want)
					}
				}
			}
			requireDemoContains(t, snapshot.Pages["vacation.html"], "Hotel Arno", "Casa delle Colline", "Hotel Giardino", "Alex Morgan", "Riley Bennett", "Colosseo", "Ponte Vecchio")
			requireDemoContains(t, snapshot.Pages["about.html"], "demo-test-version")
			requireDemoContains(t, snapshot.Pages["settings.html"], `name="deployment"`, "travel-chat", "gpt-4o", "2024-08-06", "/settings/ai/probe")
			deployment := regexp.MustCompile(`(?s)<select id="ai-deployment"[^>]*>(.*?)</select>`).
				FindSubmatch(snapshot.Pages["settings.html"])
			if len(deployment) != 2 || bytes.Count(deployment[1], []byte("<option")) < 2 {
				t.Error("Foundry dropdown must contain a prompt and a discovered deployment")
			}
			requireDemoContains(t, snapshot.Fragments["/api/background-status"], `value="3"`, `max="5"`)
			requireDemoContains(t, snapshot.Fragments[base+"/api/ideas"], "Toscana", "Lazio", "San Gimignano", "Villa Borghese")
			requireDemoContains(t, snapshot.Pages["vacation.html"], "https://en.wikipedia.org/wiki/San_Gimignano", "https://www.tripadvisor.com/Attraction_Review-")
			requireDemoContains(t, snapshot.Fragments[base+"/api/budget"], "1781", "819", "450", "Casa delle Colline", demoID(21).String())
			requireDemoContains(t, snapshot.Pages["export.html"], "Hotel Arno", "Colosseo")
			if language == "en" {
				requireDemoContains(t, snapshot.Pages["index.html"], "Italian spring escape", "A weekend in Copenhagen", "Autumn in Porto")
				requireDemoContains(t, snapshot.Fragments[base+"/cheatsheet"], "Hello", "Thank you", "Can we leave our bags here?", "Two coffees, please.")
			} else {
				requireDemoContains(t, snapshot.Pages["index.html"], "Frühling in Italien", "Ein Wochenende in Kopenhagen", "Herbst in Porto")
				requireDemoContains(t, snapshot.Fragments[base+"/cheatsheet"], "Hallo", "Danke", "Können wir unser Gepäck hier lassen?", "Zwei Kaffee, bitte.")
			}
			sheet := snapshot.Fragments[base+"/cheatsheet"]
			requireDemoContains(t, sheet, "Ciao", "Buongiorno", "Grazie", "GRAH-tsyeh", "Possiamo lasciare qui i bagagli?")
			if got := bytes.Count(sheet, []byte("<tbody")); got != 1 {
				t.Errorf("built-in and custom phrases must share one table body, got %d", got)
			}
			rows := regexp.MustCompile(`(?s)<tbody id="cheatsheet-rows"[^>]*>(.*?)</tbody>`).FindSubmatch(sheet)
			if len(rows) != 2 || bytes.Count(rows[1], []byte("<tr>")) != 24 {
				t.Error("unified cheatsheet must render all 24 phrase rows")
			}
			countLabels := regexp.MustCompile(`data-day-count="([^"]+)"[^>]*>\((\d+)\)</span>`).
				FindAllSubmatch(snapshot.Pages["vacation.html"], -1)
			firstDayCount := 0
			for _, label := range countLabels {
				if string(label[2]) == "3" {
					firstDayCount++
				}
			}
			if firstDayCount != 2 {
				t.Errorf("day and week views must both show the first day's three planned items, got %d labels", firstDayCount)
			}
			var payload overviewMapPayload
			if err := json.Unmarshal(snapshot.MapData, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Lodgings) != 3 || payload.Center == nil || payload.Geography.Pending || payload.Geography.Error {
				t.Fatalf("unexpected map payload: %+v", payload)
			}
			for i, marker := range payload.Lodgings {
				if marker.ID != demoID(20+i).String() || marker.Lat == 0 || marker.Lng == 0 {
					t.Errorf("unexpected lodging marker: %+v", marker)
				}
			}
			for _, forbidden := range []string{"Colosseo", "Ponte Vecchio", `"items"`, `"sights"`} {
				if bytes.Contains(snapshot.MapData, []byte(forbidden)) {
					t.Errorf("overview map includes POI data %q", forbidden)
				}
			}
			assertDemoFragmentsComplete(t, snapshot)
			repeat, err := RenderDemo(t.Context(), language, "demo-test-version")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot, repeat) {
				t.Error("repeated rendering changed snapshot data")
				for name, body := range snapshot.Pages {
					demoFirstDifference(t, name, body, repeat.Pages[name])
				}
				for name, body := range snapshot.Fragments {
					demoFirstDifference(t, name, body, repeat.Fragments[name])
				}
			}
		})
	}
}

func demoFirstDifference(t *testing.T, name string, a, b []byte) {
	t.Helper()
	if bytes.Equal(a, b) {
		return
	}
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			t.Errorf("%s differs at %d: %q != %q", name, i, a[max(0, i-40):min(len(a), i+80)], b[max(0, i-40):min(len(b), i+80)])
			return
		}
	}
	t.Errorf("%s lengths differ: %d != %d", name, len(a), len(b))
}

func requireDemoContains(t *testing.T, body []byte, values ...string) {
	t.Helper()
	text := html.UnescapeString(string(body))
	for _, value := range values {
		if !strings.Contains(text, value) {
			t.Errorf("rendered HTML missing %q", value)
		}
	}
}

func assertDemoFragmentsComplete(t *testing.T, snapshot *DemoSnapshot) {
	t.Helper()
	check := func(name string, body []byte) {
		for _, url := range demoFragmentURLs(body) {
			if _, ok := snapshot.Fragments[url]; !ok {
				t.Errorf("%s: missing nested fragment %s", name, url)
			}
		}
	}
	for name, body := range snapshot.Pages {
		check(name, body)
	}
	for url, body := range snapshot.Fragments {
		check(url, body)
	}
	base := "/vacations/" + snapshot.TripID
	for _, suffix := range []string{"/cheatsheet", "/api/destination-info", "/api/ideas", "/api/overview", "/api/budget"} {
		if len(snapshot.Fragments[base+suffix]) == 0 {
			t.Errorf("missing fragment %s", suffix)
		}
	}
	for _, prefix := range []string{base + "/api/daycards?day=", base + "/api/dayroute?day="} {
		n := 0
		for url := range snapshot.Fragments {
			if strings.HasPrefix(url, prefix) {
				n++
			}
		}
		if n != 7 {
			t.Errorf("want seven day fragments for %s, got %d", prefix, n)
		}
	}
}

func TestDemoFixtureCalculationsAndIsolation(t *testing.T) {
	t.Parallel()
	ctx := i18n.NewContext(t.Context(), i18n.NewLocalizer(i18n.LangEN))
	s, trip, err := newDemoServer(ctx, i18n.LangEN, "fixture-version")
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	if s.geo != nil || s.destImg != nil || s.routing.Enabled() || s.geography.stop != nil || s.cheatsheetStop != nil {
		t.Fatal("demo has an active provider or worker")
	}
	v, err := s.loadVacationFull(ctx, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	v.Participants, err = s.store.ListVacationParticipants(ctx, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Participants) != 2 || len(v.Lodgings) != 3 || v.Nights() != 6 || len(v.Items) != 14 {
		t.Fatalf("incomplete fixture: %+v", v)
	}
	var scheduled, untimed, ideas int
	regions := make(map[string]int)
	for _, item := range v.Items {
		if item.Day != nil {
			scheduled++
			if !item.Timed() {
				untimed++
			}
			continue
		}
		ideas++
		regions[item.Region]++
		if len(item.Links) != 1 || models.ValidateItemLinks(item.Links) != nil {
			t.Errorf("idea reference was not preserved: %+v", item)
		}
	}
	if scheduled != 10 || untimed != 2 || ideas != 4 || regions["Toscana"] != 2 || regions["Lazio"] != 2 {
		t.Errorf("incomplete activity fixture: scheduled=%d untimed=%d ideas=%d regions=%v", scheduled, untimed, ideas, regions)
	}
	vacations, err := s.store.ListVacations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var planned, archived int
	for _, vacation := range vacations {
		if vacation.Archived {
			archived++
		} else {
			planned++
		}
	}
	if planned != 2 || archived != 1 {
		t.Errorf("want two planned and one archived trip, got %d and %d", planned, archived)
	}
	budget := newBudgetView(v, s.budgetInputFor(ctx, v.Items, v.Participants))
	if budget.Spent != 1781 || budget.Remaining != 819 || budget.Unassigned != 450 || budget.UnassignedCount != 1 || budget.ExpenseCount != 13 {
		t.Fatalf("incorrect real budget calculations: %+v", budget)
	}
	foundUnassigned := false
	for _, expense := range budget.Expenses {
		if expense.PayerID == "" {
			foundUnassigned = expense.SourceID == demoID(21).String() && expense.Title == "Casa delle Colline" && expense.Source != ""
		}
	}
	if !foundUnassigned {
		t.Fatal("unassigned expense lost its editable source")
	}
	sheet, err := s.store.GetCheatsheet(ctx, trip.ID, "en")
	if err != nil || len(sheet.Phrases) != 22 {
		t.Fatalf("cached cheatsheet: %v, %+v", err, sheet)
	}
	if err := sheet.Validate(); err != nil {
		t.Fatal(err)
	}
	custom, err := s.store.ListCustomCheatsheetPhrases(ctx, &models.CustomTravelPhrase{
		VacationID: trip.ID, SourceLanguage: "en", DestinationKey: sheet.DestinationKey, TargetLanguage: sheet.Language,
	})
	if err != nil || len(custom) != 2 {
		t.Fatalf("custom phrases: %v, %+v", err, custom)
	}
	_, tz := s.regionSettings(ctx)
	dayRoute := s.dayRoute(ctx, i18n.FromContext(ctx), tz, v, v.StartDate)
	if len(dayRoute.Legs) != 3 || dayRoute.Approx != 3 || dayRoute.Routed != 0 || dayRoute.Missing != 0 || dayRoute.Duration != "" || !strings.Contains(dayRoute.Base, "Hotel Arno") {
		t.Fatalf("day route must show estimated lodging-to-POI distances, not invented times: %+v", dayRoute)
	}
	for _, leg := range dayRoute.Legs {
		if leg.Distance == "" || leg.Duration != "" || !leg.Approx {
			t.Errorf("invalid estimated route leg: %+v", leg)
		}
	}
	base := "/vacations/" + trip.ID.String()
	counts, err := demoGET(ctx, s, base+"/api/daycounts")
	if err != nil {
		t.Fatal(err)
	}
	var dayCounts map[string]int
	if err := json.Unmarshal(counts, &dayCounts); err != nil {
		t.Fatal(err)
	}
	if len(dayCounts) != 7 {
		t.Fatalf("want counts for seven days, got %v", dayCounts)
	}
	for i, want := range []int{3, 1, 2, 1, 0, 2, 1} {
		key := v.StartDate.AddDate(0, 0, i).Format("2006-01-02")
		if dayCounts[key] != want {
			t.Errorf("day %s has %d planned items, want %d", key, dayCounts[key], want)
		}
	}
	for _, url := range []string{base + "/api/ideas", base + "/api/overview-map", base + "/cheatsheet", "/api/background-status"} {
		if _, err := demoGET(ctx, s, url); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.store.CountPendingCheatsheetJobs(ctx)
	if err != nil || jobs != 0 || len(s.geography.queue) != 0 {
		t.Fatalf("GET enqueued background work: translations=%d, geography=%d, err=%v", jobs, len(s.geography.queue), err)
	}
	for _, url := range []string{"/api/geocode?q=Roma", "/api/destination-image", "/settings/ai/discover"} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, url, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("network endpoint %s is reachable: %d", url, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodPost, "/vacations", nil))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("demo router accepts writes: %d", rec.Code)
	}
	if err := s.foundry.Refresh(ctx); err == nil {
		t.Fatal("demo discovery did not fail closed")
	}
	if _, err := s.foundry.DoChat(ctx, foundry.Target{}, nil); err == nil {
		t.Fatal("demo generation did not fail closed")
	}
}

func TestDemoInvalidLanguageAndCancellation(t *testing.T) {
	t.Parallel()
	if snapshot, err := RenderDemo(t.Context(), "not-a-language", "test"); err == nil || snapshot != nil {
		t.Fatal("invalid language accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if snapshot, err := RenderDemo(ctx, "en", "test"); err == nil || snapshot != nil {
		t.Fatal("cancelled rendering succeeded")
	}
}

func TestDemoIgnoresRuntimeEnvironment(t *testing.T) {
	t.Parallel()
	// A subprocess provides hostile runtime settings without mutating the
	// environment of parallel tests or touching any configured resource.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestDemoEnvironmentChild$")
	cmd.Env = append(os.Environ(),
		"VP_DEMO_TEST_CHILD=1", "APP_ENV=production", "CSRF_KEY=invalid",
		"DB_PATH=/not-a-demo-database/production.db", "HTTP_ADDR=invalid-address",
		"VP_API_KEY=demo-hostile-secret", "GEOCODER_API_KEY=demo-hostile-secret",
		"ROUTER_API_KEY=demo-hostile-secret", "AZURE_CLIENT_SECRET=demo-hostile-secret",
		"AZURE_RESOURCE_ID=invalid-resource", "AZURE_TENANT_ID=invalid-tenant",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("environment-independent render failed: %v\n%s", err, output)
	}
}

func TestDemoEnvironmentChild(t *testing.T) {
	if os.Getenv("VP_DEMO_TEST_CHILD") != "1" {
		t.Skip("subprocess helper")
	}
	snapshot, err := RenderDemo(t.Context(), "en", "isolated-version")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range snapshot.Pages {
		for _, forbidden := range []string{"demo-hostile-secret", "production.db", "invalid-resource", "invalid-tenant"} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Errorf("%s exposes runtime configuration %q", name, forbidden)
			}
		}
	}
}
