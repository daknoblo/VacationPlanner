package server

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/daknoblo/vacationplanner/internal/i18n"
	"github.com/daknoblo/vacationplanner/internal/models"
	"github.com/daknoblo/vacationplanner/internal/store"
)

func demoID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("de000000-0000-4000-8000-%012d", n))
}

func demoPtr[T any](v T) *T { return &v }

func demoText(lang i18n.Lang, en, de string) string {
	if lang == i18n.LangDE {
		return de
	}
	return en
}

func seedDemo(ctx context.Context, st store.Store, lang i18n.Lang) (*models.Vacation, error) {
	start := time.Date(time.Now().UTC().Year()+1, time.May, 12, 0, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return start.AddDate(0, 0, n) }
	// Rome uses UTC+2 throughout this May itinerary.
	at := func(n, hour int) time.Time { return day(n).Add(time.Duration(hour-2) * time.Hour) }
	trip := &models.Vacation{
		ID: demoID(1), Title: demoText(lang, "Italian spring escape", "Frühling in Italien"),
		Destination: "Toscana & Roma, Italia", StartDate: start, EndDate: day(6),
		Latitude: demoPtr(43.7696), Longitude: demoPtr(11.2558), Budget: demoPtr(2600.0), People: 2,
		Notes: demoText(lang,
			"Fictional itinerary. Leave time for espresso breaks; reserve museum tickets ahead. Bring comfortable shoes and refillable water bottles.",
			"Erfundene Reiseplanung. Zeit für Espressopausen lassen; Museumstickets vorab reservieren. Bequeme Schuhe und wiederbefüllbare Wasserflaschen mitnehmen."),
	}
	for _, v := range []*models.Vacation{trip, {
		ID: demoID(2), Title: demoText(lang, "A weekend in Copenhagen", "Ein Wochenende in Kopenhagen"),
		Destination: "København, Danmark", StartDate: day(80), EndDate: day(83), People: 2, Budget: demoPtr(900.0),
	}, {
		ID: demoID(3), Title: demoText(lang, "Autumn in Porto", "Herbst in Porto"), Destination: "Porto, Portugal",
		StartDate: time.Date(2024, 10, 4, 0, 0, 0, 0, time.UTC),
		EndDate:   time.Date(2024, 10, 8, 0, 0, 0, 0, time.UTC), People: 2, Budget: demoPtr(1200.0),
	}} {
		if err := st.CreateVacation(ctx, v); err != nil {
			return nil, err
		}
	}
	if err := st.ArchiveVacation(ctx, demoID(3), start); err != nil {
		return nil, err
	}
	alex, riley := demoID(10), demoID(11)
	for _, p := range []models.Person{
		{ID: alex, Name: "Alex Morgan", Color: "#3975db", SortOrder: 0},
		{ID: riley, Name: "Riley Bennett", Color: "#d86e40", SortOrder: 1},
	} {
		if err := st.CreatePerson(ctx, &p); err != nil {
			return nil, err
		}
	}
	if err := st.SetVacationParticipants(ctx, trip.ID, []uuid.UUID{alex, riley}); err != nil {
		return nil, err
	}
	for _, l := range []models.Lodging{
		{ID: demoID(20), Name: "Hotel Arno", Location: "Firenze, Toscana", Latitude: demoPtr(43.7723), Longitude: demoPtr(11.2497),
			CheckIn: at(0, 13), CheckOut: at(2, 10), Cost: demoPtr(320.0), PaidBy: &alex},
		{ID: demoID(21), Name: "Casa delle Colline", Location: "Siena, Toscana", Latitude: demoPtr(43.3227), Longitude: demoPtr(11.3272),
			CheckIn: at(2, 15), CheckOut: at(5, 10), Cost: demoPtr(450.0)},
		{ID: demoID(22), Name: "Hotel Giardino", Location: "Roma, Lazio", Latitude: demoPtr(41.9008), Longitude: demoPtr(12.4958),
			CheckIn: at(5, 15), CheckOut: at(6, 10), Cost: demoPtr(180.0), PaidBy: &riley},
	} {
		l.VacationID = trip.ID
		l.Notes = demoText(lang, "Fictional accommodation booking.", "Erfundene Unterkunftsbuchung.")
		if l.ID == demoID(21) {
			l.Notes = demoText(lang,
				"Fictional apartment booking with a kitchen and courtyard. Assign the payer when the booking is settled.",
				"Erfundene Ferienwohnung mit Küche und Innenhof. Die zahlende Person nach der Abrechnung zuordnen.")
		}
		if err := st.CreateLodging(ctx, &l); err != nil {
			return nil, err
		}
	}
	for _, tr := range []models.TravelSegment{
		{ID: demoID(30), Kind: models.TravelArrival, Mode: "train", FromLocation: "Bologna Centrale", ToLocation: "Firenze Santa Maria Novella",
			FromLat: demoPtr(44.506), FromLng: demoPtr(11.342), ToLat: demoPtr(43.776), ToLng: demoPtr(11.248),
			DepartAt: demoPtr(at(0, 10)), ArriveAt: demoPtr(at(0, 11)), DistanceM: demoPtr(97000.0), DurationS: demoPtr(3600), Cost: demoPtr(240.0), PaidBy: &alex},
		{ID: demoID(31), Kind: models.TravelDeparture, Mode: "train", FromLocation: "Roma Termini", ToLocation: "Bologna Centrale",
			FromLat: demoPtr(41.901), FromLng: demoPtr(12.501), ToLat: demoPtr(44.506), ToLng: demoPtr(11.342),
			DepartAt: demoPtr(at(6, 16)), ArriveAt: demoPtr(at(6, 19)), DistanceM: demoPtr(360000.0), DurationS: demoPtr(10800), Cost: demoPtr(220.0), PaidBy: &riley},
	} {
		tr.VacationID = trip.ID
		if err := st.CreateTravelSegment(ctx, &tr); err != nil {
			return nil, err
		}
	}
	items := []models.Item{
		{Title: "Duomo di Firenze", Category: "Point of Interest", Day: demoPtr(day(0)), StartMin: 14 * 60, EndMin: 15 * 60,
			Latitude: demoPtr(43.7731), Longitude: demoPtr(11.256), Cost: demoPtr(30.0), PaidBy: &alex, Region: "Toscana"},
		{Title: "Ponte Vecchio", Category: "Point of Interest", Day: demoPtr(day(0)), StartMin: 16 * 60, EndMin: 17 * 60,
			Latitude: demoPtr(43.768), Longitude: demoPtr(11.2531), Region: "Toscana"},
		{Title: demoText(lang, "Dinner by the Arno", "Abendessen am Arno"), Category: "Food", Day: demoPtr(day(0)),
			Latitude: demoPtr(43.7675), Longitude: demoPtr(11.254), Cost: demoPtr(44.0), PaidBy: &riley, Region: "Toscana"},
		{Title: "Galleria degli Uffizi", Category: "Activity", Day: demoPtr(day(1)), StartMin: 10 * 60, EndMin: 12 * 60,
			Latitude: demoPtr(43.7678), Longitude: demoPtr(11.2553), Cost: demoPtr(68.0), PaidBy: &alex, Region: "Toscana"},
		{Title: "Firenze → Siena", Category: "Activity", Day: demoPtr(day(2)), StartMin: 11 * 60, EndMin: 13 * 60,
			Latitude: demoPtr(43.331), Longitude: demoPtr(11.322), Cost: demoPtr(56.0), PaidBy: &riley, Region: "Toscana"},
		{Title: "Piazza del Campo", Category: "Point of Interest", Day: demoPtr(day(2)), StartMin: 16 * 60, EndMin: 17 * 60,
			Latitude: demoPtr(43.3186), Longitude: demoPtr(11.3317), Region: "Toscana"},
		{Title: demoText(lang, "Tuscan picnic", "Picknick in der Toskana"), Category: "Food", Day: demoPtr(day(3)),
			Latitude: demoPtr(43.315), Longitude: demoPtr(11.328), Cost: demoPtr(18.0), PaidBy: &alex, Region: "Toscana"},
		{Title: "Siena → Roma", Category: "Activity", Day: demoPtr(day(5)), StartMin: 10 * 60, EndMin: 14 * 60,
			Latitude: demoPtr(41.901), Longitude: demoPtr(12.501), Cost: demoPtr(75.0), PaidBy: &riley, Region: "Lazio"},
		{Title: "Colosseo", Category: "Point of Interest", Day: demoPtr(day(5)), StartMin: 16 * 60, EndMin: 18 * 60,
			Latitude: demoPtr(41.8902), Longitude: demoPtr(12.4922), Cost: demoPtr(48.0), PaidBy: &alex, Region: "Lazio"},
		{Title: demoText(lang, "Farewell lunch", "Abschiedsessen"), Category: "Food", Day: demoPtr(day(6)), StartMin: 12 * 60, EndMin: 13 * 60,
			Latitude: demoPtr(41.899), Longitude: demoPtr(12.497), Cost: demoPtr(32.0), PaidBy: &riley, Region: "Lazio"},
		{Title: "San Gimignano", Category: "Point of Interest", Latitude: demoPtr(43.4677), Longitude: demoPtr(11.0435), Region: "Toscana",
			Links: []models.ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/San_Gimignano"}}},
		{Title: "Giardino di Boboli", Category: "Activity", Latitude: demoPtr(43.7629), Longitude: demoPtr(11.2487), Region: "Toscana",
			Links: []models.ItemLink{{Kind: "tripadvisor", URL: "https://www.tripadvisor.com/Attraction_Review-g187895-d243431-Reviews-Giardino_di_Boboli-Florence_Tuscany.html"}}},
		{Title: "Villa Borghese", Category: "Activity", Latitude: demoPtr(41.9142), Longitude: demoPtr(12.4923), Region: "Lazio",
			Links: []models.ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/Villa_Borghese_gardens"}}},
		{Title: "Trastevere", Category: "Food", Latitude: demoPtr(41.8893), Longitude: demoPtr(12.4699), Region: "Lazio",
			Links: []models.ItemLink{{Kind: "wikipedia", URL: "https://en.wikipedia.org/wiki/Trastevere"}}},
	}
	for i := range items {
		it := &items[i]
		it.ID, it.VacationID = demoID(100+i), trip.ID
		it.RegionManual = true
		it.Location = it.Title + ", Italia"
		if err := st.CreateItem(ctx, it); err != nil {
			return nil, err
		}
	}
	for _, setting := range []struct{ key, value string }{
		{settingTimezone, "Europe/Rome"}, {settingWeekStart, "monday"}, {settingCurrency, "€"},
	} {
		if err := st.PutSetting(ctx, setting.key, setting.value); err != nil {
			return nil, err
		}
	}
	if err := seedDemoCheatsheet(ctx, st, trip, lang); err != nil {
		return nil, err
	}
	return trip, nil
}

func seedDemoCheatsheet(ctx context.Context, st store.Store, trip *models.Vacation, lang i18n.Lang) error {
	key, err := models.CheatsheetDestinationKey(trip)
	if err != nil {
		return err
	}
	sheet := &models.Cheatsheet{
		VacationID: trip.ID, SourceLanguage: string(lang), DestinationKey: key,
		Country: demoText(lang, "Italy", "Italien"), Language: demoText(lang, "Italian", "Italienisch"),
	}
	for _, phrase := range [][3]string{
		{"hello", "Ciao", "chow"}, {"good_day", "Buongiorno", "bwohn-JOR-no"},
		{"goodbye", "Arrivederci", "ah-ree-veh-DER-chee"}, {"please", "Per favore", "pehr fah-VOH-reh"},
		{"thank_you", "Grazie", "GRAH-tsyeh"}, {"yes", "Sì", "see"}, {"no", "No", "noh"},
		{"excuse_me", "Mi scusi", "mee SKOO-zee"}, {"sorry", "Mi dispiace", "mee dees-PYAH-cheh"},
		{"dont_understand", "Non capisco", "non kah-PEES-koh"}, {"english", "Parla inglese?", "PAR-lah een-GLEH-zeh"},
		{"help", "Aiuto!", "ah-YOO-toh"}, {"doctor", "Ho bisogno di un medico", "oh bee-ZOH-nyoh dee oon MEH-dee-koh"},
		{"toilet", "Dov'è il bagno?", "doh-VEH eel BAH-nyoh"}, {"water", "Acqua", "AHK-kwah"},
		{"menu", "Il menù, per favore", "eel meh-NOO pehr fah-VOH-reh"},
		{"vegetarian", "Sono vegetariano", "SOH-noh veh-jeh-tah-RYAH-noh"},
		{"bill", "Il conto, per favore", "eel KON-toh pehr fah-VOH-reh"},
		{"how_much", "Quanto costa?", "KWAN-toh KOS-tah"}, {"card", "Posso pagare con la carta?", "POS-soh pah-GAH-reh kon lah KAR-tah"},
		{"ticket", "Un biglietto, per favore", "oon bee-LYEHT-toh pehr fah-VOH-reh"},
		{"hotel", "Dov'è l'albergo?", "doh-VEH lal-BEHR-goh"},
	} {
		sheet.Phrases = append(sheet.Phrases, models.TravelPhrase{Key: phrase[0], Text: phrase[1], Pronunciation: phrase[2]})
	}
	if err := st.PutCheatsheet(ctx, sheet); err != nil {
		return err
	}
	for _, phrase := range []struct{ en, de, text, pronunciation string }{
		{"Can we leave our bags here?", "Können wir unser Gepäck hier lassen?", "Possiamo lasciare qui i bagagli?", "pos-SYAH-moh lah-SHAH-reh kwee ee bah-GAH-lyee"},
		{"Two coffees, please.", "Zwei Kaffee, bitte.", "Due caffè, per favore.", "DOO-eh kaf-FEH pehr fah-VOH-reh"},
	} {
		if err := st.PutCustomCheatsheetPhrase(ctx, &models.CustomTravelPhrase{
			VacationID: trip.ID, SourceLanguage: string(lang), DestinationKey: key, TargetLanguage: sheet.Language,
			Original: demoText(lang, phrase.en, phrase.de), Text: phrase.text, Pronunciation: phrase.pronunciation,
		}); err != nil {
			return err
		}
	}
	return nil
}
