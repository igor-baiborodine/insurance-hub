package application

import (
	"context"
	"errors"
	"testing"
)

func TestStatementImports_ArchivesOnlyAfterDurablePosting(t *testing.T) {
	// given
	snapshot := importSnapshot()
	objects := &fakeStatementObjects{source: snapshot}
	poster := &fakeStatementPoster{receipt: StatementReceipt{ID: 7, State: StatementPosted}}
	receipts := &fakeStatementReceipts{
		archived: StatementReceipt{ID: 7, State: StatementArchived},
	}
	imports := newStatementImports(t, objects, poster, receipts)

	// when
	receipt, err := imports.Import(t.Context(), snapshot.SourceName)

	// then
	if err != nil || receipt != receipts.archived {
		t.Fatalf("Import() = (%+v, %v)", receipt, err)
	}
	if !objects.archived || poster.calls != 1 || receipts.archiveCalls != 1 ||
		receipts.resumeCalls != 0 {
		t.Fatalf("calls = objects:%t post:%d archive:%d resume:%d",
			objects.archived, poster.calls, receipts.archiveCalls, receipts.resumeCalls)
	}
}

func TestStatementImports_ResumesProcessedOnlyReceiptWithoutPosting(t *testing.T) {
	// given
	snapshot := importSnapshot()
	objects := &fakeStatementObjects{
		sourceErr: ErrStatementNotFound,
		processed: snapshot,
	}
	poster := &fakeStatementPoster{}
	receipts := &fakeStatementReceipts{
		resumed:  StatementReceipt{ID: 9, State: StatementPosted},
		archived: StatementReceipt{ID: 9, State: StatementArchived},
	}
	imports := newStatementImports(t, objects, poster, receipts)

	// when
	receipt, err := imports.Import(t.Context(), snapshot.SourceName)

	// then
	if err != nil || receipt != receipts.archived {
		t.Fatalf("Import() = (%+v, %v)", receipt, err)
	}
	if poster.calls != 0 || receipts.resumeCalls != 1 || receipts.archiveCalls != 1 {
		t.Fatalf("calls = post:%d resume:%d archive:%d",
			poster.calls, receipts.resumeCalls, receipts.archiveCalls)
	}
}

func TestStatementImports_ReconcilesMatchingSourceForArchivedReceipt(t *testing.T) {
	// given
	snapshot := importSnapshot()
	objects := &fakeStatementObjects{source: snapshot}
	poster := &fakeStatementPoster{
		receipt: StatementReceipt{ID: 11, State: StatementArchived},
	}
	receipts := &fakeStatementReceipts{
		archived: StatementReceipt{ID: 11, State: StatementArchived},
	}
	imports := newStatementImports(t, objects, poster, receipts)

	// when
	receipt, err := imports.Import(t.Context(), snapshot.SourceName)

	// then
	if err != nil || receipt != receipts.archived || !objects.archived ||
		receipts.archiveCalls != 1 {
		t.Fatalf("archived-source reconciliation = (%+v, %v, %t, %d)",
			receipt, err, objects.archived, receipts.archiveCalls)
	}
}

func TestStatementImports_PreservesReviewAndRetryBoundaries(t *testing.T) {
	retryErr := errors.New("retry boundary")
	tests := []struct {
		name        string
		objects     *fakeStatementObjects
		poster      *fakeStatementPoster
		receipts    *fakeStatementReceipts
		wantReceipt StatementReceipt
		wantErr     error
	}{
		{
			name:    "review does not archive",
			objects: &fakeStatementObjects{source: importSnapshot()},
			poster: &fakeStatementPoster{
				receipt: StatementReceipt{ID: 3, State: StatementReview},
			},
			receipts:    &fakeStatementReceipts{},
			wantReceipt: StatementReceipt{ID: 3, State: StatementReview},
		},
		{
			name:     "failed SQL commit leaves objects",
			objects:  &fakeStatementObjects{source: importSnapshot()},
			poster:   &fakeStatementPoster{err: retryErr},
			receipts: &fakeStatementReceipts{},
			wantErr:  retryErr,
		},
		{
			name: "archive interruption returns posted receipt",
			objects: &fakeStatementObjects{
				source:     importSnapshot(),
				archiveErr: retryErr,
			},
			poster: &fakeStatementPoster{
				receipt: StatementReceipt{ID: 5, State: StatementPosted},
			},
			receipts:    &fakeStatementReceipts{},
			wantReceipt: StatementReceipt{ID: 5, State: StatementPosted},
			wantErr:     retryErr,
		},
		{
			name: "orphaned processed object cannot post",
			objects: &fakeStatementObjects{
				sourceErr: ErrStatementNotFound,
				processed: importSnapshot(),
			},
			poster:   &fakeStatementPoster{},
			receipts: &fakeStatementReceipts{resumeErr: ErrStatementConflict},
			wantErr:  ErrStatementConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			imports := newStatementImports(t, test.objects, test.poster, test.receipts)

			// when
			receipt, err := imports.Import(t.Context(), importSnapshot().SourceName)

			// then
			if receipt != test.wantReceipt || !errors.Is(err, test.wantErr) {
				t.Fatalf("Import() = (%+v, %v), want (%+v, %v)",
					receipt, err, test.wantReceipt, test.wantErr)
			}
			if test.objects.archived || test.receipts.archiveCalls != 0 {
				t.Fatal("incomplete outcome advanced archival")
			}
		})
	}
}

func TestNewStatementImports_RequiresDependencies(t *testing.T) {
	objects := &fakeStatementObjects{}
	poster := &fakeStatementPoster{}
	receipts := &fakeStatementReceipts{}
	for _, test := range []struct {
		objects  StatementObjects
		poster   StatementPoster
		receipts StatementReceipts
	}{
		{poster: poster, receipts: receipts},
		{objects: objects, receipts: receipts},
		{objects: objects, poster: poster},
	} {
		if imports, err := NewStatementImports(
			test.objects, test.poster, test.receipts,
		); imports != nil || err == nil {
			t.Fatalf("NewStatementImports() = (%v, %v)", imports, err)
		}
	}
}

func newStatementImports(
	t *testing.T,
	objects StatementObjects,
	poster StatementPoster,
	receipts StatementReceipts,
) *StatementImports {
	t.Helper()
	imports, err := NewStatementImports(objects, poster, receipts)
	if err != nil {
		t.Fatal(err)
	}
	return imports
}

func importSnapshot() StatementSnapshot {
	return StatementSnapshot{
		SourceName: "bankStatements_2018_8_2.csv",
		SHA256:     "40992d034ec49cffd117038441a38329ae18bdba1d8bf56a1dd6a67010bf8f4a",
		Content: []byte(
			"TransactionId,TransactionType,AccountingDate,AccountNumber,Amount\r\n" +
				"1,A,2018-08-01,231232132131,10.21\r\n",
		),
	}
}

type fakeStatementObjects struct {
	source       StatementSnapshot
	sourceErr    error
	processed    StatementSnapshot
	processedErr error
	archiveErr   error
	archived     bool
}

func (objects *fakeStatementObjects) ReadStatement(
	context.Context,
	string,
) (StatementSnapshot, error) {
	return objects.source, objects.sourceErr
}

func (objects *fakeStatementObjects) ReadProcessedStatement(
	context.Context,
	string,
) (StatementSnapshot, error) {
	return objects.processed, objects.processedErr
}

func (objects *fakeStatementObjects) ArchiveStatement(
	context.Context,
	StatementSnapshot,
) error {
	if objects.archiveErr != nil {
		return objects.archiveErr
	}
	objects.archived = true
	return nil
}

type fakeStatementPoster struct {
	receipt StatementReceipt
	err     error
	calls   int
}

func (poster *fakeStatementPoster) Post(
	context.Context,
	BankStatement,
) (StatementReceipt, error) {
	poster.calls++
	return poster.receipt, poster.err
}

type fakeStatementReceipts struct {
	resumed      StatementReceipt
	resumeErr    error
	archived     StatementReceipt
	archiveErr   error
	resumeCalls  int
	archiveCalls int
}

func (receipts *fakeStatementReceipts) ResumeStatement(
	context.Context,
	StatementSnapshot,
) (StatementReceipt, error) {
	receipts.resumeCalls++
	return receipts.resumed, receipts.resumeErr
}

func (receipts *fakeStatementReceipts) ArchiveStatement(
	_ context.Context,
	_ StatementReceipt,
	_ StatementSnapshot,
) (StatementReceipt, error) {
	receipts.archiveCalls++
	return receipts.archived, receipts.archiveErr
}
