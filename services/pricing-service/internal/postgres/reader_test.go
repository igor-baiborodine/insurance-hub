package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
)

func TestDecodeTariff_PreservesOrderNullabilityAndDecimalScale(t *testing.T) {
	// given
	base := []byte(`[
		{"position":1,"coverCode":"C1","condition":null,"formula":"10.00"},
		{"position":2,"coverCode":"C2","condition":"","formula":"20"}
	]`)
	markup := []byte(`[
		{"position":1,"kind":"perc_markup","condition":null,"factor":"1.50"},
		{"position":2,"kind":"perc_markup","condition":"FLOOD == 'YES'","factor":"2.00"}
	]`)

	// when
	tariff, err := decodeTariff("ORDERED", base, markup)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if tariff.Code != "ORDERED" || len(tariff.BaseRules) != 2 ||
		tariff.BaseRules[0].CoverCode != "C1" || tariff.BaseRules[0].Condition != nil ||
		tariff.BaseRules[1].CoverCode != "C2" || tariff.BaseRules[1].Condition == nil ||
		*tariff.BaseRules[1].Condition != "" {
		t.Fatalf("decoded base rules = %+v", tariff.BaseRules)
	}
	if len(tariff.MarkupRules) != 2 || tariff.MarkupRules[0].Condition != nil ||
		tariff.MarkupRules[0].Factor.String() != "1.50" ||
		tariff.MarkupRules[1].Factor.String() != "2.00" {
		t.Fatalf("decoded markup rules = %+v", tariff.MarkupRules)
	}
}

func TestDecodeTariff_RejectsUnusableTariffs(t *testing.T) {
	validBase := `[{"position":1,"coverCode":"C1","condition":null,"formula":"10"}]`
	validMarkup := `[{"position":1,"kind":"perc_markup","condition":null,"factor":"1.00"}]`
	tests := []struct {
		name   string
		code   string
		base   string
		markup string
	}{
		{name: "empty code", code: "", base: validBase, markup: validMarkup},
		{name: "missing base", code: "TEST", base: `[]`, markup: validMarkup},
		{
			name:   "base position gap",
			code:   "TEST",
			base:   strings.Replace(validBase, `"position":1`, `"position":2`, 1),
			markup: validMarkup,
		},
		{
			name:   "missing cover",
			code:   "TEST",
			base:   strings.Replace(validBase, `"C1"`, `null`, 1),
			markup: validMarkup,
		},
		{name: "missing markup", code: "TEST", base: validBase, markup: `[]`},
		{
			name:   "invalid factor",
			code:   "TEST",
			base:   validBase,
			markup: strings.Replace(validMarkup, `"1.00"`, `"invalid"`, 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			tariff, err := decodeTariff(
				test.code,
				[]byte(test.base),
				[]byte(test.markup),
			)

			// then
			if tariff.Code != "" || !errors.Is(err, application.ErrTariffInvalid) {
				t.Fatalf("tariff=%+v error=%v", tariff, err)
			}
		})
	}
}

func TestNewReader_RejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name           string
		acquireTimeout time.Duration
		queryTimeout   time.Duration
		want           string
	}{
		{name: "acquire timeout", queryTimeout: time.Second, want: "acquire timeout"},
		{name: "query timeout", acquireTimeout: time.Second, want: "query timeout"},
		{
			name:           "pool",
			acquireTimeout: time.Second,
			queryTimeout:   time.Second,
			want:           "pool is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			reader, err := NewReader(nil, test.acquireTimeout, test.queryTimeout)

			// then
			if reader != nil || err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("reader=%v error=%v", reader, err)
			}
		})
	}
}

func TestNewReader_AcceptsCompleteSettings(t *testing.T) {
	// given
	pool := &pgxpool.Pool{}

	// when
	reader, err := NewReader(pool, time.Second, 2*time.Second)

	// then
	if err != nil || reader == nil {
		t.Fatalf("NewReader() = (%v, %v)", reader, err)
	}
	if reader.pool != pool || reader.acquireTimeout != time.Second ||
		reader.queryTimeout != 2*time.Second {
		t.Errorf("reader settings = %+v", reader)
	}
}

func TestClassifyReaderFailure_DistinguishesCancellationAndUnavailableStorage(t *testing.T) {
	// given
	storageFailure := errors.New("private database detail")

	// when
	unavailable := classifyReaderFailure(context.Background(), "query tariff", storageFailure)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := classifyReaderFailure(canceledCtx, "query tariff", storageFailure)

	// then
	if !errors.Is(unavailable, application.ErrReaderUnavailable) ||
		!errors.Is(
			unavailable,
			storageFailure,
		) || strings.Contains(unavailable.Error(), storageFailure.Error()) {
		t.Fatalf("unavailable classification = %v", unavailable)
	}
	if !errors.Is(canceled, context.Canceled) ||
		errors.Is(canceled, application.ErrReaderUnavailable) {
		t.Fatalf("canceled classification = %v", canceled)
	}
}

func TestDecodeTariff_RejectsMalformedAndIncompleteRuleFields(t *testing.T) {
	// given
	validBase := `[{"position":1,"coverCode":"C1","condition":null,"formula":"10"}]`
	validMarkup := `[{"position":1,"kind":"perc_markup","condition":null,"factor":"1.00"}]`
	tests := []struct {
		name, base, markup string
	}{
		{name: "malformed base", base: `{`, markup: validMarkup},
		{
			name:   "empty cover",
			base:   strings.Replace(validBase, `"C1"`, `""`, 1),
			markup: validMarkup,
		},
		{
			name:   "missing formula",
			base:   strings.Replace(validBase, `"10"`, `null`, 1),
			markup: validMarkup,
		},
		{name: "malformed markup", base: validBase, markup: `{`},
		{
			name:   "markup position gap",
			base:   validBase,
			markup: strings.Replace(validMarkup, `"position":1`, `"position":2`, 1),
		},
		{
			name:   "missing kind",
			base:   validBase,
			markup: strings.Replace(validMarkup, `"perc_markup"`, `null`, 1),
		},
		{
			name:   "missing factor",
			base:   validBase,
			markup: strings.Replace(validMarkup, `"1.00"`, `null`, 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			tariff, err := decodeTariff("TEST", []byte(test.base), []byte(test.markup))

			// then
			if tariff.Code != "" || !errors.Is(err, application.ErrTariffInvalid) {
				t.Fatalf("tariff=%+v error=%v", tariff, err)
			}
		})
	}
}
