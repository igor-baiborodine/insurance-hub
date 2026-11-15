package service

import (
	"context"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

func TestNewProductionGeneration_RequiresDependencyObserver(t *testing.T) {
	// when
	generation, err := newProductionGeneration(context.Background(), config.Events{}, 1, nil)

	// then
	if err == nil || generation != nil {
		t.Fatalf("generation = %v, error = %v", generation, err)
	}
}
