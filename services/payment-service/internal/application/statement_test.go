package application

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseBankStatement_PreservesKnownCreditAndEqualRows(t *testing.T) {
	// given
	content := "TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n" +
		"\"known,credit\",A,2018-08-01,231232132131,10.21\r\n" +
		"2,A,2018-08-01,231232132131,10.21\r\n"
	snapshot := StatementSnapshot{
		SourceName: "bankStatements_2018_8_2.csv",
		SHA256:     strings.Repeat("a", 64),
		Content:    []byte(content),
	}

	// when
	statement, err := ParseBankStatement(snapshot)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if statement.Snapshot.SourceName != snapshot.SourceName ||
		statement.Snapshot.SHA256 != snapshot.SHA256 ||
		string(statement.Snapshot.Content) != content {
		t.Fatalf("snapshot changed: %+v", statement.Snapshot)
	}
	if len(statement.Credits) != 2 {
		t.Fatalf("credits = %d, want 2", len(statement.Credits))
	}
	wantDate := time.Date(2018, time.August, 1, 0, 0, 0, 0, time.UTC)
	for index, credit := range statement.Credits {
		if !credit.EffectiveDate.Equal(wantDate) ||
			credit.AccountNumber != "231232132131" || credit.Amount != "10.21" {
			t.Errorf("credit %d = %+v", index, credit)
		}
	}
}

func TestStatementSourceName_RoundTripsValidUTCDate(t *testing.T) {
	// given
	businessDate := time.Date(
		2018,
		time.August,
		2,
		23,
		30,
		0,
		0,
		time.FixedZone("west", -4*60*60),
	)

	// when
	name := StatementSourceName(businessDate)
	parsed, valid := StatementSourceDate(name)

	// then
	if name != "bankStatements_2018_8_3.csv" || !valid ||
		!parsed.Equal(time.Date(2018, time.August, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("statement source = (%q, %v, %t)", name, parsed, valid)
	}
}

func TestStatementSourceDate_RejectsInvalidCalendarDate(t *testing.T) {
	if _, valid := StatementSourceDate("bankStatements_2026_2_29.csv"); valid {
		t.Fatal("invalid calendar date accepted")
	}
}

func TestParseBankStatement_RejectsWholeInvalidFileWithoutExposingRows(t *testing.T) {
	// given
	header := "TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n"
	valid := "1,A,2018-08-01,231232132131,10.21\r\n"
	tests := []struct {
		name    string
		content string
	}{
		{"invalid date", header + "1,A,2018-08-32,DATE-MARKER,10.21\r\n"},
		{"fraction would round", header + "1,A,2018-08-01,ROUND-MARKER,10.211\r\n"},
		{
			"integer exceeds numeric precision",
			header + "1,A,2018-08-01,LARGE-MARKER,100000000000000000.00\r\n",
		},
		{"malformed final row", header + valid + "2,A,2018-08-01,PARSE-MARKER,\"10.21\r\n"},
		{"missing header", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			statement, err := ParseBankStatement(
				StatementSnapshot{Content: []byte(test.content)},
			)

			// then
			if !errors.Is(err, ErrStatementInvalid) {
				t.Fatalf("error = %v, want invalid statement", err)
			}
			if len(statement.Credits) != 0 || len(statement.Snapshot.Content) != 0 {
				t.Fatalf("partial statement returned: %+v", statement)
			}
			if strings.Contains(err.Error(), "MARKER") ||
				strings.Contains(err.Error(), "10.21") {
				t.Fatalf("error exposes statement data: %q", err)
			}
		})
	}
}

func TestNumeric19Scale2_CanonicalizesOnlyExactValues(t *testing.T) {
	// given
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"10.21", "10.21", true},
		{"00010.2", "10.20", true},
		{"+.5", "0.50", true},
		{"-0", "0.00", true},
		{"99999999999999999.99", "99999999999999999.99", true},
		{"10.211", "", false},
		{"100000000000000000.00", "", false},
		{"1e2", "", false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			// when
			got, ok := numeric19Scale2(test.input)

			// then
			if got != test.want || ok != test.ok {
				t.Errorf("numeric19Scale2(%q) = (%q, %t), want (%q, %t)",
					test.input, got, ok, test.want, test.ok)
			}
		})
	}
}
