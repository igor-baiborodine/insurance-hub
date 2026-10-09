//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

func TestPostgresReader(t *testing.T) {
	// given
	image := os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE")
	if image != integrationtest.DefaultPostgresImage {
		t.Fatalf("PRODUCT_TEST_POSTGRES_IMAGE does not contain the pinned image")
	}
	if selection := os.Getenv(
		"PRODUCT_INTEGRATION_FIXTURE_SET",
	); selection != string(
		integrationtest.FixtureQA,
	) {
		t.Fatalf("fixture selection = %q, want qa", selection)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := integrationtest.ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := integrationtest.Start(ctx, integrationtest.Options{
		FixtureRoot: baselineRoot,
		Image:       image,
	})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	reader, err := open(
		ctx,
		harness.RuntimeDatabaseURL(),
		1,
		3*time.Second,
		2*time.Second,
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	readerClosed := false
	t.Cleanup(func() {
		if !readerClosed {
			reader.Close()
		}
	})
	if reader.pool.Config().MaxConns != 1 || reader.pool.Config().MinConns != 0 {
		t.Fatalf(
			"pool bounds = %d/%d, want 1/0",
			reader.pool.Config().MaxConns,
			reader.pool.Config().MinConns,
		)
	}

	t.Run("reads the complete QA catalog through the restricted role", func(t *testing.T) {
		// given
		fixture, loadErr := harness.LoadCatalog(ctx, integrationtest.FixtureQA)
		if loadErr != nil {
			t.Fatalf("load QA catalog: %v", loadErr)
		}

		// when
		products, listErr := reader.ListProducts(ctx)
		product, found, getErr := reader.GetProduct(ctx, "CAR")

		// then
		if listErr != nil || getErr != nil || !found || product.Code != "CAR" {
			t.Fatalf(
				"QA reads: count=%d product=%+v found=%t list-error=%v get-error=%v",
				len(products),
				product,
				found,
				listErr,
				getErr,
			)
		}
		codes := make([]string, len(products))
		for index := range products {
			codes[index] = products[index].Code
		}
		sort.Strings(codes)
		if !reflect.DeepEqual(codes, fixture.DataIdentity.Codes) {
			t.Errorf("codes = %v, want %v", codes, fixture.DataIdentity.Codes)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("returns empty single and missing results", func(t *testing.T) {
		// given
		if resetErr := harness.ResetRows(ctx, nil); resetErr != nil {
			t.Fatalf("reset empty: %v", resetErr)
		}

		// when
		empty, emptyErr := reader.ListProducts(ctx)
		missing, found, missingErr := reader.GetProduct(ctx, "CAR")

		// then
		if emptyErr != nil || empty == nil || len(empty) != 0 {
			t.Errorf("empty list = %v, error = %v", empty, emptyErr)
		}
		if missingErr != nil || found || missing.Code != "" {
			t.Errorf(
				"missing product = %+v, found=%t, error=%v",
				missing,
				found,
				missingErr,
			)
		}

		// given
		if resetErr := harness.ResetRows(ctx, []integrationtest.FixtureRow{
			{
				Code:                      "ONLY",
				RawLosslessDefinitionJSON: `{"covers":[{"sumInsured":12345678901234567890.2300}]}`,
			},
		}); resetErr != nil {
			t.Fatalf("reset single: %v", resetErr)
		}

		// when
		single, singleErr := reader.ListProducts(ctx)

		// then
		if singleErr != nil || len(single) != 1 || single[0].Code != "ONLY" ||
			len(single[0].Covers) != 1 || single[0].Covers[0].SumInsured == nil ||
			single[0].Covers[0].SumInsured.String() != "12345678901234567890.2300" {
			t.Errorf("single list = %+v, error=%v", single, singleErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("preserves exact and parameterized lookup codes", func(t *testing.T) {
		// given
		rows := []integrationtest.FixtureRow{
			{Code: "Case", RawLosslessDefinitionJSON: `{}`},
			{Code: " spaced ", RawLosslessDefinitionJSON: `{}`},
			{Code: "CAR' OR '1'='1", RawLosslessDefinitionJSON: `{}`},
		}
		if resetErr := harness.ResetRows(ctx, rows); resetErr != nil {
			t.Fatalf("reset lookup rows: %v", resetErr)
		}

		for _, row := range rows {
			// when
			product, found, getErr := reader.GetProduct(ctx, row.Code)

			// then
			if getErr != nil || !found || product.Code != row.Code {
				t.Errorf(
					"exact lookup %q: product=%+v found=%t error=%v",
					row.Code,
					product,
					found,
					getErr,
				)
			}
		}
		for _, code := range []string{"case", "spaced", "CAR"} {
			if _, found, getErr := reader.GetProduct(
				ctx,
				code,
			); getErr != nil ||
				found {
				t.Errorf(
					"non-exact lookup %q: found=%t error=%v",
					code,
					found,
					getErr,
				)
			}
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("rejects a corrupt row without returning a partial list", func(t *testing.T) {
		// given
		if resetErr := harness.ResetRows(ctx, []integrationtest.FixtureRow{
			{Code: "GOOD", RawLosslessDefinitionJSON: `{}`},
			{
				Code:                      "CORRUPT",
				RawLosslessDefinitionJSON: `{"covers":[{"unexpected":true}]}`,
			},
		}); resetErr != nil {
			t.Fatalf("reset corrupt rows: %v", resetErr)
		}

		// when
		products, listErr := reader.ListProducts(ctx)
		product, found, getErr := reader.GetProduct(ctx, "CORRUPT")

		// then
		if products != nil || !errors.Is(listErr, application.ErrInvalidDefinition) {
			t.Errorf("corrupt list = %+v, error=%v", products, listErr)
		}
		if product.Code != "" || found ||
			!errors.Is(getErr, application.ErrInvalidDefinition) {
			t.Errorf("corrupt get = %+v, found=%t, error=%v", product, found, getErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("preserves cancellation and releases the pool", func(t *testing.T) {
		// given
		canceledCtx, cancelCall := context.WithCancel(context.Background())
		cancelCall()

		// when
		products, listErr := reader.ListProducts(canceledCtx)

		// then
		if products != nil || !errors.Is(listErr, context.Canceled) ||
			errors.Is(listErr, application.ErrUnavailable) {
			t.Errorf("canceled list = %+v, error=%v", products, listErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	t.Run("bounds connection acquisition", func(t *testing.T) {
		// given
		heldConnection, acquireErr := reader.pool.Acquire(ctx)
		if acquireErr != nil {
			t.Fatalf("hold only connection: %v", acquireErr)
		}
		originalTimeout := reader.acquireTimeout
		reader.acquireTimeout = 25 * time.Millisecond

		// when
		products, listErr := reader.ListProducts(ctx)

		// then
		reader.acquireTimeout = originalTimeout
		heldConnection.Release()
		if products != nil || !errors.Is(listErr, context.DeadlineExceeded) ||
			errors.Is(listErr, application.ErrUnavailable) {
			t.Errorf("bounded acquisition list = %+v, error=%v", products, listErr)
		}
		assertNoAcquiredConnections(t, reader)
	})

	// when
	reader.Close()
	readerClosed = true
	products, listErr := reader.ListProducts(ctx)

	// then
	if products != nil || !errors.Is(listErr, application.ErrUnavailable) {
		t.Errorf("closed-pool list = %+v, error=%v", products, listErr)
	}
}

func assertNoAcquiredConnections(t *testing.T, reader *Reader) {
	t.Helper()
	if acquired := reader.pool.Stat().AcquiredConns(); acquired != 0 {
		t.Errorf("acquired connections = %d, want 0", acquired)
	}
}
