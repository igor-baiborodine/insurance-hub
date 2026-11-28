package application

import (
	"context"
	"errors"
	"time"
)

// StatementState is the durable progress of one logical source and digest.
type StatementState string

const (
	// StatementReview holds bytes that require explicit review without posting credits.
	StatementReview StatementState = "review"
	// StatementPosted records credits committed before object-store archival.
	StatementPosted StatementState = "posted"
	// StatementArchived records a verified processed copy and completed source cleanup.
	StatementArchived StatementState = "archived"
)

// StatementReceipt identifies the durable recovery record for one statement snapshot.
type StatementReceipt struct {
	ID    int64
	State StatementState
}

// StatementStore atomically resolves accounts and stores a receipt with all accepted credits.
type StatementStore interface {
	StoreStatement(context.Context, BankStatement, time.Time) (StatementReceipt, error)
}

// StatementPostings commits validated bank statements through the Payment-owned transaction.
type StatementPostings struct {
	statements StatementStore
	now        func() time.Time
}

// NewStatementPostings wires statement persistence and its UTC creation-date clock.
func NewStatementPostings(
	statements StatementStore,
	now func() time.Time,
) (*StatementPostings, error) {
	if statements == nil || now == nil {
		return nil, errors.New("statement postings: all dependencies are required")
	}
	return &StatementPostings{statements: statements, now: now}, nil
}

// Post stores one durable outcome without exposing statement contents in failures.
func (postings *StatementPostings) Post(
	ctx context.Context,
	statement BankStatement,
) (StatementReceipt, error) {
	if !validStatementIdentity(statement.Snapshot) {
		return StatementReceipt{}, ErrStatementInvalid
	}
	instant := postings.now().UTC()
	creationDate := time.Date(
		instant.Year(), instant.Month(), instant.Day(), 0, 0, 0, 0, time.UTC,
	)
	return postings.statements.StoreStatement(ctx, statement, creationDate)
}

func validStatementIdentity(snapshot StatementSnapshot) bool {
	if !ValidStatementSourceName(snapshot.SourceName) ||
		len(snapshot.SHA256) != 64 {
		return false
	}
	for _, character := range snapshot.SHA256 {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
