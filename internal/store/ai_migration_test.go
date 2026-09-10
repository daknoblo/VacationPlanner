package store

import (
	"context"
	"testing"
	"time"

	"github.com/daknoblo/vacationplanner/internal/models"
)

func TestLegacyAISettingsMigrationPreservesFoundryAndTrips(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	// Recreate the pre-upgrade settings while leaving the existing schema intact.
	if _, err := st.db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = 17"); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"ai.base_url": "https://obsolete.invalid/v1", "ai.model": "obsolete", "ai.api_version": "old",
		"ai.foundry.chat.account": "production-chat", "foundry.account.catalog": "cached-metadata", "region.timezone": "Europe/Berlin",
	} {
		if err := st.PutSetting(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	trip := &models.Vacation{Title: "Preserved trip", Destination: "Berlin", StartDate: time.Now(), EndDate: time.Now()}
	if err := st.CreateVacation(ctx, trip); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	settings, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ai.base_url", "ai.model", "ai.api_version"} {
		if _, exists := settings[key]; exists {
			t.Errorf("obsolete setting retained: %s", key)
		}
	}
	if settings["ai.foundry.chat.account"] != "production-chat" || settings["foundry.account.catalog"] != "cached-metadata" || settings["region.timezone"] != "Europe/Berlin" {
		t.Fatal("migration changed unrelated or Foundry settings")
	}
	if got, err := st.GetVacation(ctx, trip.ID); err != nil || got.Title != trip.Title {
		t.Fatal("migration changed trip data")
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal("migration is not idempotent", err)
	}
}
