package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/geo"
	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestLodgingNameAndTypeDisambiguateSharedAddress(t *testing.T) {
	l := &models.Lodging{Name: "INNSIDE Hamburg Hafen", Location: "Recha-Lübke-Damm 30, 20097 Hamburg"}
	hotel := geo.Result{
		Name: "Innside Hamburg Hafen", Type: "house", Class: "tourism", OSMValue: "hotel",
		Street: "Recha-Lübke-Damm", HouseNumber: "30", Postcode: "20097", City: "Hamburg",
		Lat: 53.545466, Lng: 10.0149687,
	}
	restaurant := hotel
	restaurant.Name, restaurant.Class, restaurant.OSMValue = "Werft", "amenity", "restaurant"
	restaurant.Lat, restaurant.Lng = 53.5453789, 10.0152571
	college := hotel
	college.Name, college.Class, college.OSMValue = "Ausbildungszentrum", "amenity", "college"
	college.Lat, college.Lng = 53.545974, 10.0127498
	for _, results := range [][]geo.Result{
		{hotel, restaurant, college}, {college, hotel, restaurant}, {restaurant, college, hotel},
	} {
		got, ok := matchLodging(l, results)
		if !ok || got.Name != hotel.Name || got.Lat != hotel.Lat || got.Lng != hotel.Lng {
			t.Fatalf("did not select the corroborated lodging: %+v, %v", got, ok)
		}
	}
	conflictingHotel := hotel
	conflictingHotel.Lat++
	if _, ok := matchLodging(l, []geo.Result{hotel, conflictingHotel, restaurant}); ok {
		t.Fatal("multiple equally supported lodging locations were accepted")
	}
	l.Location = "Other Road 30, 20097 Hamburg"
	if _, ok := matchLodging(l, []geo.Result{hotel, restaurant, college}); ok {
		t.Fatal("hotel name overrode a conflicting address")
	}
}

func TestSharedAddressWorkerEnrichesHotelNotRestaurant(t *testing.T) {
	s, st, v := newGeographyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"name":"Restaurant","display_name":"Restaurant, Quay 12, Brighton","type":"restaurant","lat":"50.01","lon":"-1.01","address":{"road":"Quay","house_number":"12","city":"Brighton"}},
			{"name":"Blue Harbour Hotel","display_name":"Blue Harbour Hotel, Quay 12, Brighton","type":"hotel","lat":"50","lon":"-1","address":{"road":"Quay","house_number":"12","city":"Brighton"}}
		]`))
	})
	ctx := context.Background()
	l := &models.Lodging{VacationID: v.ID, Name: "Blue Harbour Hotel", Location: "Quay 12, Brighton", CheckIn: time.Now(), CheckOut: time.Now().Add(24 * time.Hour)}
	if err := st.CreateLodging(ctx, l); err != nil {
		t.Fatal(err)
	}
	stop := s.StartGeographyWorker(ctx)
	defer stop()
	s.queueGeography(v.ID, "en")
	status := waitGeography(t, s, v.ID)
	got, err := st.GetLodging(ctx, l.ID)
	if err != nil || !got.HasCoords() || *got.Latitude != 50 || *got.Longitude != -1 || status.UpdatedCount != 1 {
		t.Fatalf("hotel marker coordinates not persisted: %+v %+v %v", got, status, err)
	}
}
