package integrationtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ScenarioDisposition string

const (
	DispositionCurrentTest  ScenarioDisposition = "current-test"
	DispositionLaterStep    ScenarioDisposition = "later-step"
	DispositionNonTransport ScenarioDisposition = "non-transport"
	DispositionOutsideGo    ScenarioDisposition = "outside-go-backend"
)

type ScenarioRegistration struct {
	Owner       string
	Disposition ScenarioDisposition
}

// ScenarioRegistry explicitly assigns every accepted manifest, case, and nested scenario ID.
// Adding a fixture case therefore fails validation until its ownership is reviewed here.
func ScenarioRegistry() map[string]ScenarioRegistration {
	registry := make(map[string]ScenarioRegistration)
	add := func(owner string, disposition ScenarioDisposition, ids string) {
		for _, id := range strings.Fields(ids) {
			if _, duplicate := registry[id]; duplicate {
				panic("duplicate baseline scenario registration: " + id)
			}
			registry[id] = ScenarioRegistration{Owner: owner, Disposition: disposition}
		}
	}

	add("fixture integrity and captured topology evidence", DispositionNonTransport, `
		INV-LOCAL-001 INV-QA-001 CHECK-FIXTURE-COVERAGE-001 CHECK-DRIFT-001
		CHECK-SNAPSHOT-INVALIDATION-001 CHECK-SHARED-STATE-001 CHECK-HANDOFF-001
		CHECK-CONSUMERS-001 DECISION-PARITY-001 DECISION-TRANSPORT-001
		DECISION-SECURITY-001
	`)
	add("PostgreSQL reader and Step 14 catalog parity", DispositionCurrentTest, `
		DATA-QA-001
	`)
	add("Step 14 accepted local catalog replay", DispositionLaterStep, `
		DATA-LOCAL-001
	`)
	add("Steps 12 and 14 direct HTTP parity", DispositionLaterStep, `
		HTTP-DIRECT-LIST-001 HTTP-DIRECT-GET-CAR-001 HTTP-DIRECT-GET-FAI-001
		HTTP-DIRECT-GET-HSI-001 HTTP-DIRECT-GET-TRI-001 HTTP-EMPTY-CATALOG-001
		HTTP-SINGLE-CATALOG-001 HTTP-MISSING-001 HTTP-LOOKUP-PATHS-001
	`)
	add("Steps 10 through 14 data semantics", DispositionCurrentTest, `
		DATA-DECIMAL-001 DATA-QUESTION-VARIANTS-001 DATA-PRESENCE-DEFAULTS-001
		DATA-UNKNOWN-FIELDS-001 DATA-UNKNOWN-SUBTYPE-001 DATA-ORDERING-001
	`)
	add("Steps 11 through 15 safe backend failures", DispositionLaterStep, `
		FAIL-DECODE-001 FAIL-DATABASE-001
	`)
	add("Step 18 anonymous direct access", DispositionLaterStep, `
		ACCESS-DIRECT-LIST-001 ACCESS-DIRECT-GET-001
	`)
	add("Steps 8, 9, and 17 restricted database role", DispositionLaterStep, `
		DB-READ-001 DB-DML-DENY-001 DB-DDL-DENY-001
	`)
	add("Java gateway and later migration tickets", DispositionOutsideGo, `
		HTTP-GATEWAY-LIST-001 HTTP-GATEWAY-GET-CAR-001 HTTP-GATEWAY-GET-FAI-001
		HTTP-GATEWAY-GET-HSI-001 HTTP-GATEWAY-GET-TRI-001 FAIL-GATEWAY-BACKEND-001
		ACCESS-GATEWAY-LIST-001 ACCESS-GATEWAY-GET-001 ACCESS-PROPAGATION-001
	`)

	add("current routing tests and Steps 12 through 14", DispositionLaterStep, `
		FAIL-PRODUCT-EMPTY-LIST-001 FAIL-PRODUCT-SINGLE-LIST-001
		FAIL-PRODUCT-SINGLE-GET-001 FAIL-PRODUCT-MISSING-001
		FAIL-PRODUCT-LOOKUP-LOWERCASE-001 FAIL-PRODUCT-LOOKUP-WHITESPACE-001
		FAIL-PRODUCT-LOOKUP-ENCODED-SLASH-001 FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001
		FAIL-PRODUCT-LOOKUP-EMPTY-SEGMENT-001 FAIL-PRODUCT-DECODE-TYPE-001
		FAIL-PRODUCT-DECODE-STRUCTURE-001 FAIL-PRODUCT-DATABASE-LIST-001
		FAIL-PRODUCT-DATABASE-GET-001 HTTP-EMPTY-DIRECT-LIST-001
		HTTP-SINGLE-DIRECT-LIST-001 HTTP-SINGLE-DIRECT-GET-001
		HTTP-MISSING-DIRECT-001 HTTP-LOOKUP-LOWERCASE-001
		HTTP-LOOKUP-WHITESPACE-001 HTTP-LOOKUP-ENCODED-SLASH-001
		HTTP-LOOKUP-BAD-ENCODING-001 HTTP-LOOKUP-EMPTY-SEGMENT-001
		FAIL-DECODE-TYPE-001 FAIL-DECODE-STRUCTURE-001 FAIL-DATABASE-LIST-001
		FAIL-DATABASE-GET-001
	`)
	add("Steps 10 and 14 data-edge parity", DispositionCurrentTest, `
		DATA-EDGES-RICH-001 DATA-EDGES-ABSENT-001 DATA-EDGES-EMPTY-001
		DATA-EDGES-PRIMITIVE-NULL-001 DATA-EDGES-CHOICE-PRESENCE-001
		DATA-EDGES-PRODUCT-ORDER-001 DATA-EDGES-NULL-COVERS-001
		DATA-EDGES-NULL-QUESTIONS-001 DATA-EDGES-UNKNOWN-COVER-001
		DATA-EDGES-UNKNOWN-QUESTION-001 DATA-EDGES-UNKNOWN-CHOICE-001
		DATA-EDGES-SUBTYPE-UNKNOWN-001 DATA-EDGES-SUBTYPE-ABSENT-001
		DATA-EDGES-SUBTYPE-NULL-001 DATA-DECIMAL-FRACTION-001
		DATA-DECIMAL-PRECISION-001 DATA-DECIMAL-SCALE-001 DATA-DECIMAL-ZERO-001
		DATA-DECIMAL-NULL-001 DATA-QUESTION-CHOICE-001 DATA-QUESTION-DATE-001
		DATA-QUESTION-NUMERIC-001 DATA-UNKNOWN-PRODUCT-FIELD-001
		DATA-UNKNOWN-COVER-FIELD-001 DATA-UNKNOWN-QUESTION-FIELD-001
		DATA-UNKNOWN-CHOICE-FIELD-001 DATA-ORDER-COVERS-001
		DATA-ORDER-QUESTIONS-001 DATA-ORDER-CHOICES-001 DATA-DEFAULT-ZERO-001
		DATA-DEFAULT-FALSE-001 DATA-DEFAULT-NULL-001 DATA-PRESENCE-ABSENT-001
		DATA-PRESENCE-EMPTY-001 DATA-PRESENCE-NULL-001 DATA-ORDER-PRODUCTS-001
		DATA-SUBTYPE-UNKNOWN-001 DATA-SUBTYPE-ABSENT-001 DATA-SUBTYPE-NULL-001
	`)
	add("Steps 8 and 17 database constraint proof", DispositionNonTransport, `
		DATA-EDGES-SQL-NULL-DEFINITION-001 DATA-EDGES-SQL-NULL-CODE-001
	`)
	add("Step 18 direct anonymous access", DispositionLaterStep, `
		ACCESS-DIRECT-LIST-NONE-001 ACCESS-DIRECT-LIST-VALID-001
		ACCESS-DIRECT-LIST-MALFORMED-001 ACCESS-DIRECT-LIST-SIGNATURE-001
		ACCESS-DIRECT-LIST-EXPIRED-001 ACCESS-DIRECT-LIST-NBF-001
		ACCESS-DIRECT-LIST-ISSUER-001 ACCESS-DIRECT-LIST-AUDIENCE-001
		ACCESS-DIRECT-LIST-ROLE-001 ACCESS-DIRECT-GET-NONE-001
		ACCESS-DIRECT-GET-VALID-001 ACCESS-DIRECT-GET-MALFORMED-001
		ACCESS-DIRECT-GET-SIGNATURE-001 ACCESS-DIRECT-GET-EXPIRED-001
		ACCESS-DIRECT-GET-NBF-001 ACCESS-DIRECT-GET-ISSUER-001
		ACCESS-DIRECT-GET-AUDIENCE-001 ACCESS-DIRECT-GET-ROLE-001
		ACCESS-DIRECT-LIST-INVALID-001 ACCESS-DIRECT-GET-INVALID-001
	`)
	add("Java gateway and later migration tickets", DispositionOutsideGo, `
		FAIL-GATEWAY-EMPTY-LIST-001 FAIL-GATEWAY-SINGLE-LIST-001
		FAIL-GATEWAY-SINGLE-GET-001 FAIL-GATEWAY-MISSING-001
		FAIL-GATEWAY-LOOKUP-LOWERCASE-001 FAIL-GATEWAY-LOOKUP-WHITESPACE-001
		FAIL-GATEWAY-LOOKUP-ENCODED-SLASH-001 FAIL-GATEWAY-LOOKUP-BAD-ENCODING-001
		FAIL-GATEWAY-LOOKUP-EMPTY-SEGMENT-001 FAIL-GATEWAY-DECODE-TYPE-001
		FAIL-GATEWAY-DECODE-STRUCTURE-001 FAIL-GATEWAY-DATABASE-LIST-001
		FAIL-GATEWAY-DATABASE-GET-001 FAIL-GATEWAY-LIST-ERROR-001
		FAIL-GATEWAY-GET-ERROR-001 FAIL-GATEWAY-LIST-UNAVAILABLE-001
		FAIL-GATEWAY-GET-UNAVAILABLE-001 HTTP-EMPTY-GATEWAY-LIST-001
		HTTP-SINGLE-GATEWAY-LIST-001 HTTP-SINGLE-GATEWAY-GET-001
		HTTP-MISSING-GATEWAY-001 ACCESS-GATEWAY-LIST-VALID-001
		ACCESS-GATEWAY-LIST-MISSING-001 ACCESS-GATEWAY-LIST-MALFORMED-001
		ACCESS-GATEWAY-LIST-SIGNATURE-001 ACCESS-GATEWAY-LIST-EXPIRED-001
		ACCESS-GATEWAY-LIST-NBF-001 ACCESS-GATEWAY-LIST-ISSUER-001
		ACCESS-GATEWAY-LIST-AUDIENCE-001 ACCESS-GATEWAY-LIST-ROLE-001
		ACCESS-GATEWAY-LIST-SUBJECT-001 ACCESS-GATEWAY-GET-VALID-001
		ACCESS-GATEWAY-GET-MISSING-001 ACCESS-GATEWAY-GET-MALFORMED-001
		ACCESS-GATEWAY-GET-SIGNATURE-001 ACCESS-GATEWAY-GET-EXPIRED-001
		ACCESS-GATEWAY-GET-NBF-001 ACCESS-GATEWAY-GET-ISSUER-001
		ACCESS-GATEWAY-GET-AUDIENCE-001 ACCESS-GATEWAY-GET-ROLE-001
		ACCESS-GATEWAY-GET-SUBJECT-001
	`)
	add("Steps 8, 9, and 17 restricted database role", DispositionLaterStep, `
		DB-READ-CONNECT-001 DB-READ-LIST-001 DB-READ-GET-001
		DB-DENY-INSERT-001 DB-DENY-UPDATE-001 DB-DENY-DELETE-001
		DB-DENY-TRUNCATE-001 DB-DENY-CREATE-001 DB-DENY-ALTER-001
		DB-DENY-DROP-001
	`)
	return registry
}

var acceptedCorpusSHA256 = map[string]string{
	"manifest.json":             "6d43eea94cf113b6bcc483da69313f6ccf266440ae37af0f9c56664758c3e9d5",
	"inventory/local-dev.json":  "cc4a970509ed1754902ce272b251dc88f54f19ef354f17552326333185b1e1be",
	"inventory/qa.json":         "f0920d34256e099e5335bc35e764bb4bfa30d9b0ab2605c3737a5718453a17b2",
	"catalog/local-dev.json":    "c5ec4eb51c1a435527dcdf42cab111a3c609e82d7d996ae10022640dbf471496",
	"catalog/qa.json":           "b8a9f732f75f0b071c0295fdc77335a96e3bd19fa1a083bc4d7a1501879737f0",
	"http/local-dev.json":       "ab3e9d96c46fb86629294395dcade25f25b19d453542879d54bd6e0a8cd71333",
	"http/qa.json":              "1566c953e931e411d5b910c092a45f72f5ab7cd4c6261c5ec9037b9ba208761a",
	"access/local-dev.json":     "780245ffcfce4cf8ebefc39327d03781eeb49a03b11b0d3dff31c5cfbaa2eca2",
	"access/qa.json":            "d468bd8ca118a75e45afd38f4b93133a96f38a00ce0556f1cc379a4f5395658a",
	"access/cases.json":         "e1a60fd2dac35c69fe629a224d6bc4609148af9c4e290096165f087b9508a9f3",
	"failures/cases.json":       "fd5bbc587039e67de073741589a99dc359423119dd429d508570289869a6d0f1",
	"db-permissions/cases.json": "c8530f45d2cf9ad880c82b3421312166873195dacca83b1d2af3433a4f587e63",
	"data-edges/cases.json":     "91113d45e4ce4deecf9be58fafffd5c0ec0ca0f218b43568b2132cdb75b5f6d3",
}

// ValidateBaselineCoverage pins the accepted corpus bytes and requires exact registry coverage.
func ValidateBaselineCoverage(baselineRoot string, registry map[string]ScenarioRegistration) error {
	for relativePath, wantHash := range acceptedCorpusSHA256 {
		content, err := os.ReadFile(
			filepath.Join(baselineRoot, filepath.FromSlash(relativePath)),
		)
		if err != nil {
			return fmt.Errorf("read accepted corpus %s: %w", relativePath, err)
		}
		gotHash := sha256.Sum256(content)
		if hex.EncodeToString(gotHash[:]) != wantHash {
			return fmt.Errorf("accepted corpus identity changed: %s", relativePath)
		}
	}

	ids, err := loadAcceptedIDs(baselineRoot)
	if err != nil {
		return err
	}
	for id := range ids {
		registration, ok := registry[id]
		if !ok {
			return fmt.Errorf("accepted scenario %q has no owner or classification", id)
		}
		if registration.Owner == "" || registration.Disposition == "" {
			return fmt.Errorf("accepted scenario %q has incomplete registration", id)
		}
	}
	for id := range registry {
		if _, ok := ids[id]; !ok {
			return fmt.Errorf(
				"registered scenario %q is absent from accepted corpus",
				id,
			)
		}
	}
	return nil
}

func loadAcceptedIDs(baselineRoot string) (map[string]struct{}, error) {
	ids := make(map[string]struct{})
	manifestContent, err := os.ReadFile(filepath.Join(baselineRoot, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read baseline manifest: %w", err)
	}
	var manifest struct {
		Baseline  string `json:"baseline"`
		Scenarios []struct {
			ID string `json:"id"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(manifestContent, &manifest); err != nil {
		return nil, fmt.Errorf("decode baseline manifest: %w", err)
	}
	if manifest.Baseline != "issue-131-product-read" {
		return nil, fmt.Errorf("unexpected baseline identity %q", manifest.Baseline)
	}
	for _, scenario := range manifest.Scenarios {
		if err := addAcceptedID(ids, scenario.ID); err != nil {
			return nil, err
		}
	}

	for _, relativePath := range []string{
		"failures/cases.json", "data-edges/cases.json", "access/cases.json",
		"db-permissions/cases.json",
	} {
		content, readErr := os.ReadFile(
			filepath.Join(baselineRoot, filepath.FromSlash(relativePath)),
		)
		if readErr != nil {
			return nil, fmt.Errorf("read baseline cases %s: %w", relativePath, readErr)
		}
		var fixture struct {
			Cases []struct {
				ID          string   `json:"id"`
				ScenarioID  string   `json:"scenarioId"`
				ScenarioIDs []string `json:"scenarioIds"`
			} `json:"cases"`
		}
		if decodeErr := json.Unmarshal(content, &fixture); decodeErr != nil {
			return nil, fmt.Errorf(
				"decode baseline cases %s: %w",
				relativePath,
				decodeErr,
			)
		}
		for _, testCase := range fixture.Cases {
			for _, id := range append([]string{testCase.ID, testCase.ScenarioID}, testCase.ScenarioIDs...) {
				if id == "" {
					continue
				}
				ids[id] = struct{}{}
			}
		}
	}
	return ids, nil
}

func addAcceptedID(ids map[string]struct{}, id string) error {
	if id == "" {
		return fmt.Errorf("accepted scenario has an empty ID")
	}
	if _, duplicate := ids[id]; duplicate {
		return fmt.Errorf("accepted manifest has duplicate scenario %q", id)
	}
	ids[id] = struct{}{}
	return nil
}
