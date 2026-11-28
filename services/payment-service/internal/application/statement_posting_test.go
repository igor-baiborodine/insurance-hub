package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewStatementPostings_RequiresDependencies(t *testing.T) {
	// given
	store := &fakeStatementStore{}
	for _, test := range []struct {
		name  string
		store StatementStore
		now   func() time.Time
	}{
		{name: "missing store", now: time.Now},
		{name: "missing clock", store: store},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			postings, err := NewStatementPostings(test.store, test.now)

			// then
			if postings != nil || err == nil {
				t.Fatalf("NewStatementPostings() = (%v, %v)", postings, err)
			}
		})
	}
}

func TestStatementPostings_Post_UsesOneUTCCreationDate(t *testing.T) {
	// given
	wantReceipt := StatementReceipt{ID: 7, State: StatementPosted}
	store := &fakeStatementStore{receipt: wantReceipt}
	clockCalls := 0
	postings, err := NewStatementPostings(store, func() time.Time {
		clockCalls++
		return time.Date(
			2018, time.August, 2, 23, 30, 0, 0,
			time.FixedZone("east", 2*60*60),
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	statement := validStatement()

	// when
	receipt, err := postings.Post(t.Context(), statement)

	// then
	if err != nil || receipt != wantReceipt {
		t.Fatalf("Post() = (%+v, %v)", receipt, err)
	}
	wantDate := time.Date(2018, time.August, 2, 0, 0, 0, 0, time.UTC)
	if store.statement.Snapshot.SHA256 != statement.Snapshot.SHA256 ||
		!store.creationDate.Equal(wantDate) || store.creationDate.Location() != time.UTC {
		t.Fatalf("stored input = (%+v, %v)", store.statement, store.creationDate)
	}
	if clockCalls != 1 {
		t.Fatalf("clock calls = %d, want 1", clockCalls)
	}
}

func TestStatementPostings_Post_RejectsInvalidIdentityAndPreservesStoreFailure(t *testing.T) {
	// given
	storeErr := errors.New("storage marker")
	store := &fakeStatementStore{err: storeErr}
	postings, err := NewStatementPostings(store, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []BankStatement{
		{},
		{Snapshot: StatementSnapshot{SourceName: "statement.csv", SHA256: "UPPER" +
			strings.Repeat("a", 59)}},
	} {
		// when
		_, err := postings.Post(t.Context(), statement)

		// then
		if !errors.Is(err, ErrStatementInvalid) {
			t.Fatalf("invalid identity error = %v", err)
		}
	}

	// when
	_, err = postings.Post(t.Context(), validStatement())

	// then
	if !errors.Is(err, storeErr) {
		t.Fatalf("store error = %v, want storage marker", err)
	}
}

func validStatement() BankStatement {
	return BankStatement{Snapshot: StatementSnapshot{
		SourceName: "bankStatements_2018_8_2.csv",
		SHA256:     strings.Repeat("a", 64),
	}}
}

type fakeStatementStore struct {
	statement    BankStatement
	creationDate time.Time
	receipt      StatementReceipt
	err          error
}

func (store *fakeStatementStore) StoreStatement(
	_ context.Context,
	statement BankStatement,
	creationDate time.Time,
) (StatementReceipt, error) {
	store.statement = statement
	store.creationDate = creationDate
	return store.receipt, store.err
}
