package mvel_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/libs/mvel"
)

const acceptedTariffFixtureSHA256 = "6b0ccb0c732eaa238e874782479e7b288ef37cfeaa1456176b93ae50325bddd6"

type tariffCatalog struct {
	Tariffs []struct {
		Code string `json:"code"`
		Base []struct {
			Condition *string `json:"condition"`
			Formula   string  `json:"formula"`
		} `json:"base"`
		Markup []struct {
			Condition string `json:"condition"`
		} `json:"markup"`
	} `json:"tariffs"`
}

func TestCorpus_CompilesAcceptedExpressions(t *testing.T) {
	// given
	contents, err := os.ReadFile("testdata/accepted-tariffs.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if got := hex.EncodeToString(digest[:]); got != acceptedTariffFixtureSHA256 {
		t.Fatalf(
			"accepted tariff fixture SHA-256 = %s, want %s",
			got,
			acceptedTariffFixtureSHA256,
		)
	}
	var catalog tariffCatalog
	if err := json.Unmarshal(contents, &catalog); err != nil {
		t.Fatal(err)
	}

	// when
	formulaCount := 0
	predicateCount := 0
	for _, tariff := range catalog.Tariffs {
		for _, base := range tariff.Base {
			if _, err := mvel.CompileDecimal(base.Formula); err != nil {
				t.Errorf(
					"compile %s formula %q: %v",
					tariff.Code,
					base.Formula,
					err,
				)
			}
			formulaCount++
			if base.Condition != nil {
				if _, err := mvel.CompilePredicate(*base.Condition); err != nil {
					t.Errorf(
						"compile %s base condition %q: %v",
						tariff.Code,
						*base.Condition,
						err,
					)
				}
				predicateCount++
			}
		}
		for _, markup := range tariff.Markup {
			if _, err := mvel.CompilePredicate(markup.Condition); err != nil {
				t.Errorf(
					"compile %s markup condition %q: %v",
					tariff.Code,
					markup.Condition,
					err,
				)
			}
			predicateCount++
		}
	}

	// then
	if formulaCount != 14 || predicateCount != 10 {
		t.Fatalf(
			"compiled formulas/predicates = %d/%d, want 14/10",
			formulaCount,
			predicateCount,
		)
	}
}
