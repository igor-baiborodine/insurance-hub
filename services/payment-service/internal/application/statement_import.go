package application

import (
	"context"
	"errors"
)

var (
	// ErrStatementNotFound reports an absent source or processed statement object.
	ErrStatementNotFound = errors.New("bank statement is not found")
	// ErrStatementConflict preserves bytes that differ from the durable receipt.
	ErrStatementConflict = errors.New("bank statement requires review")
)

// StatementObjects owns source reads and digest-guarded object archival.
type StatementObjects interface {
	ReadStatement(context.Context, string) (StatementSnapshot, error)
	ReadProcessedStatement(context.Context, string) (StatementSnapshot, error)
	ArchiveStatement(context.Context, StatementSnapshot) error
}

// StatementPoster commits a fully validated source statement.
type StatementPoster interface {
	Post(context.Context, BankStatement) (StatementReceipt, error)
}

// StatementReceipts resumes and completes the durable receipt state machine.
type StatementReceipts interface {
	ResumeStatement(context.Context, StatementSnapshot) (StatementReceipt, error)
	ArchiveStatement(
		context.Context,
		StatementReceipt,
		StatementSnapshot,
	) (StatementReceipt, error)
}

// StatementImports coordinates the SQL commit before recoverable object archival.
type StatementImports struct {
	objects  StatementObjects
	poster   StatementPoster
	receipts StatementReceipts
}

// NewStatementImports wires the Payment-owned statement recovery use case.
func NewStatementImports(
	objects StatementObjects,
	poster StatementPoster,
	receipts StatementReceipts,
) (*StatementImports, error) {
	if objects == nil || poster == nil || receipts == nil {
		return nil, errors.New("statement imports: all dependencies are required")
	}
	return &StatementImports{objects: objects, poster: poster, receipts: receipts}, nil
}

// Import posts one source snapshot or resumes a matching processed-only receipt.
func (imports *StatementImports) Import(
	ctx context.Context,
	sourceName string,
) (StatementReceipt, error) {
	if !ValidStatementSourceName(sourceName) {
		return StatementReceipt{}, ErrStatementInvalid
	}
	snapshot, err := imports.objects.ReadStatement(ctx, sourceName)
	processedOnly := false
	if errors.Is(err, ErrStatementNotFound) {
		processedOnly = true
		snapshot, err = imports.objects.ReadProcessedStatement(ctx, sourceName)
	}
	if err != nil {
		return StatementReceipt{}, err
	}

	statement, err := ParseBankStatement(snapshot)
	if err != nil {
		return StatementReceipt{}, err
	}

	var receipt StatementReceipt
	if processedOnly {
		receipt, err = imports.receipts.ResumeStatement(ctx, snapshot)
	} else {
		receipt, err = imports.poster.Post(ctx, statement)
	}
	if err != nil || receipt.State == StatementReview ||
		(processedOnly && receipt.State == StatementArchived) {
		return receipt, err
	}
	if receipt.State != StatementPosted && receipt.State != StatementArchived {
		return StatementReceipt{}, ErrStatementConflict
	}
	if err := imports.objects.ArchiveStatement(ctx, snapshot); err != nil {
		return receipt, err
	}
	return imports.receipts.ArchiveStatement(ctx, receipt, snapshot)
}
