package application

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
)

var statementSourceName = regexp.MustCompile(
	`\AbankStatements_[1-9][0-9]{3}_(?:[1-9]|1[0-2])_(?:[1-9]|[12][0-9]|3[01])\.csv\z`,
)

var (
	// ErrStatementInvalid reports a statement that cannot be accepted as a complete file.
	ErrStatementInvalid = errors.New("bank statement is invalid")
	// ErrStatementLimit reports a statement that exceeds the configured snapshot bound.
	ErrStatementLimit = errors.New("bank statement exceeds the accepted size")
	// ErrStatementUnavailable reports a failed object-store snapshot read.
	ErrStatementUnavailable = errors.New("bank statement is unavailable")
)

// StatementSnapshot contains the exact source bytes and their digest from one bounded read.
type StatementSnapshot struct {
	SourceName string
	SHA256     string
	Content    []byte
}

// StatementCredit is one validated incoming payment from a statement row.
type StatementCredit struct {
	EffectiveDate time.Time
	AccountNumber string
	Amount        string
}

// BankStatement retains the accepted source snapshot and every validated credit in file order.
type BankStatement struct {
	Snapshot StatementSnapshot
	Credits  []StatementCredit
}

// ValidStatementSourceName reports the accepted unpadded UTC daily object-key shape.
func ValidStatementSourceName(sourceName string) bool {
	return len(sourceName) <= 255 && statementSourceName.MatchString(sourceName)
}

// ParseBankStatement validates a complete headered RFC 4180 statement before returning credits.
func ParseBankStatement(snapshot StatementSnapshot) (BankStatement, error) {
	reader := csv.NewReader(bytes.NewReader(snapshot.Content))
	header, err := reader.Read()
	if err != nil || len(header) < 5 {
		return BankStatement{}, ErrStatementInvalid
	}

	credits := make([]StatementCredit, 0)
	for {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || len(record) < 5 {
			return BankStatement{}, ErrStatementInvalid
		}
		effectiveDate, dateErr := time.Parse("2006-01-02", record[2])
		if dateErr != nil {
			return BankStatement{}, ErrStatementInvalid
		}
		amount, amountOK := numeric19Scale2(record[4])
		if !amountOK {
			return BankStatement{}, ErrStatementInvalid
		}
		credits = append(credits, StatementCredit{
			EffectiveDate: effectiveDate,
			AccountNumber: record[3],
			Amount:        amount,
		})
	}

	return BankStatement{Snapshot: snapshot, Credits: credits}, nil
}

func numeric19Scale2(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	negative := false
	switch value[0] {
	case '-':
		negative = true
		value = value[1:]
	case '+':
		value = value[1:]
	}
	if value == "" || strings.Count(value, ".") > 1 {
		return "", false
	}
	integer, fraction, hasPoint := strings.Cut(value, ".")
	if !hasPoint {
		fraction = ""
	}
	if integer == "" && fraction == "" {
		return "", false
	}
	if integer == "" {
		integer = "0"
	}
	if len(fraction) > 2 || !decimalDigits(integer) || !decimalDigitsOrEmpty(fraction) {
		return "", false
	}
	integer = strings.TrimLeft(integer, "0")
	if integer == "" {
		integer = "0"
	}
	if len(integer) > 17 {
		return "", false
	}
	fraction += strings.Repeat("0", 2-len(fraction))
	if negative && (integer != "0" || fraction != "00") {
		integer = "-" + integer
	}
	return integer + "." + fraction, true
}

func decimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func decimalDigitsOrEmpty(value string) bool {
	return value == "" || decimalDigits(value)
}
