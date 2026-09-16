package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/daknoblo/vacationplanner/internal/ai"
	"github.com/daknoblo/vacationplanner/internal/foundry"
	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestAISearchCentersGroupOnlyLocatedAccommodationRegions(t *testing.T) {
	v := &models.Vacation{Destination: "Denmark", Latitude: fptr(55), Longitude: fptr(10)}
	lodgings := []models.Lodging{
		{Region: "Zealand", Latitude: fptr(55), Longitude: fptr(12)},
		{Region: "Jutland", Latitude: fptr(0), Longitude: fptr(10)},
		{Region: " jutland ", Latitude: fptr(0), Longitude: fptr(30)},
		{Region: "Unlocated"},
		{Latitude: fptr(54), Longitude: fptr(9)},
		{Region: "Invalid", Latitude: fptr(math.NaN()), Longitude: fptr(9)},
	}
	centers := aiSearchCenters(i18n.NewLocalizer(i18n.LangEN), v, lodgings)
	if len(centers) != 4 || centers[0].Key != "destination" || centers[3].Key != "custom" ||
		centers[1].Name != "Jutland" || centers[2].Name != "Zealand" {
		t.Fatalf("unexpected options: %+v", centers)
	}
	if *centers[0].Lat != 55 || *centers[0].Lng != 10 ||
		math.Abs(*centers[1].Lat) > 1e-9 || math.Abs(*centers[1].Lng-20) > 1e-9 {
		t.Fatal("default destination or unweighted accommodation midpoint changed")
	}
	again := aiSearchCenters(i18n.NewLocalizer(i18n.LangDE), v, []models.Lodging{lodgings[2], lodgings[1], lodgings[0]})
	if again[1].Key != centers[1].Key || math.Abs(*again[1].Lng-*centers[1].Lng) > 1e-9 ||
		!strings.Contains(again[0].Label, "Standard") {
		t.Fatal("region identity depends on language, capitalization or lodging order")
	}
}

func TestAISearchCenterHandlesLongitudeWrapAndAmbiguity(t *testing.T) {
	loc := i18n.NewLocalizer(i18n.LangEN)
	v := &models.Vacation{Destination: "Example"}
	for _, test := range []struct {
		longitudes []float64
		want       float64
		valid      bool
	}{
		{[]float64{179, -179}, 180, true},
		{[]float64{0, 180}, 0, false},
	} {
		var lodgings []models.Lodging
		for _, lng := range test.longitudes {
			lodgings = append(lodgings, models.Lodging{Region: "Example region", Latitude: fptr(0), Longitude: fptr(lng)})
		}
		center := aiSearchCenters(loc, v, lodgings)[1]
		if test.valid {
			if center.Lat == nil || center.Lng == nil || math.Abs(math.Abs(*center.Lng)-test.want) > 1e-6 {
				t.Fatal("longitude averaging incorrectly moved the center to Greenwich")
			}
		} else if center.Lat != nil || center.Lng != nil {
			t.Fatal("invented a midpoint for opposite points")
		}
	}
}

type recommendCenterBackend struct {
	stubFoundry
	payload []byte
}

func (b *recommendCenterBackend) DoChat(_ context.Context, _ foundry.Target, payload []byte) ([]byte, error) {
	b.calls++
	b.payload = append([]byte(nil), payload...)
	return []byte(`{"choices":[{"message":{"content":"{\"suggestions\":[]}"}}]}`), nil
}

func TestRecommendationPresetsUseCurrentTripBookingsAndIgnoreHiddenCoordinates(t *testing.T) {
	s, _ := foundryTestServer(t)
	backend := &recommendCenterBackend{}
	s.ai = ai.New(backend)
	ctx := t.Context()
	if err := s.store.PutSetting(ctx, s.foundrySettingKey("chat"), "production-chat"); err != nil {
		t.Fatal(err)
	}
	v := sampleVacation()
	v.Destination, v.Latitude, v.Longitude = "Denmark", fptr(55), fptr(10)
	if err := s.store.CreateVacation(ctx, v); err != nil {
		t.Fatal(err)
	}
	lodgings := make([]models.Lodging, 0, 2)
	for _, lng := range []float64{10, 30} {
		lodging := models.Lodging{
			VacationID: v.ID, Name: "Example stay", Region: "Accommodation region",
			Latitude: fptr(0), Longitude: fptr(lng), CheckIn: v.StartDate, CheckOut: v.EndDate,
		}
		if err := s.store.CreateLodging(ctx, &lodging); err != nil {
			t.Fatal(err)
		}
		lodgings = append(lodgings, lodging)
	}
	centers := aiSearchCenters(i18n.NewLocalizer(i18n.LangEN), v, lodgings)
	path := "/vacations/" + v.ID.String() + "/ai/recommendations"
	form := url.Values{
		"ai_center": {centers[1].Key}, "ai_location": {"Wrong place"}, "ai_lat": {"80"}, "ai_lng": {"-120"},
		"radius": {"50"}, "count": {"10"}, "interests": {"Museums"},
	}
	if rec := postAISettings(s, path, form, false); rec.Code != http.StatusForbidden || backend.calls != 0 {
		t.Fatal("recommendation accepted without CSRF")
	}
	for _, test := range []struct {
		key, expected string
	}{
		{centers[1].Key, "Search center: Accommodation region (latitude 0.00000, longitude 20.00000)"},
		{"destination", "Search center: Denmark (latitude 55.00000, longitude 10.00000)"},
		{"custom", "Search center: Wrong place (latitude 80.00000, longitude -120.00000)"},
		{"", "Search center: Wrong place (latitude 80.00000, longitude -120.00000)"},
	} {
		form.Set("ai_center", test.key)
		rec := postAISettings(s, path, form, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("recommendation failed: %d %s", rec.Code, rec.Body.String())
		}
		var request struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		if err := json.Unmarshal(backend.payload, &request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) != 2 || !strings.Contains(request.Messages[1].Content, test.expected) ||
			!strings.Contains(request.Messages[1].Content, "Museums") ||
			!strings.Contains(request.Messages[1].Content, "50 km") {
			t.Fatalf("wrong search context: %s", backend.payload)
		}
	}
	if err := s.store.DeleteLodging(ctx, lodgings[0].ID); err != nil {
		t.Fatal(err)
	}
	form.Set("ai_center", centers[1].Key)
	if rec := postAISettings(s, path, form, true); rec.Code != http.StatusOK ||
		!strings.Contains(string(backend.payload), "longitude 30.00000") {
		t.Fatal("server used the stale hidden midpoint after a lodging was removed")
	}
	if err := s.store.DeleteLodging(ctx, lodgings[1].ID); err != nil {
		t.Fatal(err)
	}
	calls := backend.calls
	for _, key := range []string{centers[1].Key, "region:unknown"} {
		form.Set("ai_center", key)
		if rec := postAISettings(s, path, form, true); rec.Code != http.StatusUnprocessableEntity ||
			rec.Header().Get("HX-Retarget") != "#ai-error" || backend.calls != calls {
			t.Fatal("stale or fabricated region caused a provider call")
		}
	}
	var options []aiSearchCenter
	readTripJSON(t, s, "/vacations/"+v.ID.String()+"/api/ai-centers", &options)
	if len(options) != 2 || len(s.geography.queue) != 0 || backend.calls != calls {
		t.Fatal("reading midpoint options enqueued work or included removed regions")
	}
}
