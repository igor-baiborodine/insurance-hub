package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
)

func TestOpenPool_RejectsMissingDatabaseSettings(t *testing.T) {
	// when
	pool, err := OpenPool(t.Context(), config.Database{})

	// then
	if pool != nil || err == nil {
		t.Fatal("expected invalid settings to be rejected")
	}
}

func TestOpenPool_RejectsCanceledDial(t *testing.T) {
	// given
	credential, _ := migrationConnectionFixture(t)
	credential = strings.Replace(credential, "go_pricing_migrator", "go_pricing_reader", 1)
	t.Setenv("PRICING_DATABASE_URL", credential)
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// when
	pool, err := OpenPool(ctx, settings.Database)

	// then
	if pool != nil || err == nil || strings.Contains(err.Error(), "synthetic") {
		t.Fatal("expected a redacted reader dial failure")
	}
}
