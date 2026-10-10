package application

import (
	"context"
	"errors"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

type readerFake struct {
	list     []domain.Product
	product  domain.Product
	found    bool
	err      error
	getCode  string
	listCtx  context.Context
	getCtx   context.Context
	listRuns int
	getRuns  int
}

func (reader *readerFake) ListProducts(ctx context.Context) ([]domain.Product, error) {
	reader.listRuns++
	reader.listCtx = ctx
	return reader.list, reader.err
}

func (reader *readerFake) GetProduct(
	ctx context.Context,
	code string,
) (domain.Product, bool, error) {
	reader.getRuns++
	reader.getCtx = ctx
	reader.getCode = code
	return reader.product, reader.found, reader.err
}

func TestListProducts_ReturnsWholeResultOrError(t *testing.T) {
	// given
	reader := &readerFake{}
	useCase, err := NewListProducts(reader)
	if err != nil {
		t.Fatal(err)
	}

	// when
	empty, err := useCase.Execute(context.Background())

	// then
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("empty list: products=%v error=%v", empty, err)
	}
	reader.list = []domain.Product{{Code: "CAR"}, {Code: "FAI"}}
	products, err := useCase.Execute(context.Background())
	if err != nil || len(products) != 2 || products[0].Code != "CAR" ||
		products[1].Code != "FAI" {
		t.Errorf("complete list: products=%v error=%v", products, err)
	}
	storageErr := errors.New("storage failure")
	reader.err = errors.Join(ErrUnavailable, storageErr)
	partial, err := useCase.Execute(context.Background())
	if partial != nil || !errors.Is(err, ErrUnavailable) || !errors.Is(err, storageErr) ||
		reader.listRuns != 3 {
		t.Errorf("failed list: products=%v error=%v runs=%d", partial, err, reader.listRuns)
	}
}

func TestGetProduct_PreservesOpaqueCodeAndAbsence(t *testing.T) {
	// given
	reader := &readerFake{product: domain.Product{Code: "a/b "}, found: true}
	useCase, err := NewGetProduct(reader)
	if err != nil {
		t.Fatal(err)
	}

	// when
	product, err := useCase.Execute(context.Background(), "a/b ")

	// then
	if err != nil || product.Code != "a/b " || reader.getCode != "a/b " || reader.getRuns != 1 {
		t.Errorf(
			"exact lookup: product=%v code=%q runs=%d error=%v",
			product,
			reader.getCode,
			reader.getRuns,
			err,
		)
	}
	reader.found = false
	_, err = useCase.Execute(context.Background(), "A/B ")
	if !errors.Is(err, ErrProductNotFound) || reader.getCode != "A/B " {
		t.Errorf("missing exact code: code=%q error=%v", reader.getCode, err)
	}
	_, err = useCase.Execute(context.Background(), "")
	if !errors.Is(err, ErrCodeRequired) || reader.getRuns != 2 {
		t.Errorf("empty code: runs=%d error=%v", reader.getRuns, err)
	}
}

func TestUseCases_PreserveCancellationAndReaderFailures(t *testing.T) {
	// given
	reader := &readerFake{err: errors.Join(ErrInvalidDefinition, context.DeadlineExceeded)}
	list, _ := NewListProducts(reader)
	get, _ := NewGetProduct(reader)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// when / then
	if _, err := list.Execute(
		ctx,
	); !errors.Is(err, ErrInvalidDefinition) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("list error classification: %v", err)
	}
	if _, err := get.Execute(
		ctx,
		"CAR",
	); !errors.Is(err, ErrInvalidDefinition) ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("get error classification: %v", err)
	}
	if reader.listCtx != ctx || reader.getCtx != ctx {
		t.Error("caller context was not propagated unchanged")
	}
	cancel()
	if _, err := list.Execute(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("list cancellation: %v", err)
	}
	if _, err := get.Execute(ctx, "CAR"); !errors.Is(err, context.Canceled) {
		t.Errorf("get cancellation: %v", err)
	}
	if reader.listRuns != 1 || reader.getRuns != 1 {
		t.Errorf(
			"canceled calls reached reader: list=%d get=%d",
			reader.listRuns,
			reader.getRuns,
		)
	}
}

func TestUseCases_NeedReader(t *testing.T) {
	if _, err := NewListProducts(nil); !errors.Is(err, ErrReaderRequired) {
		t.Errorf("list constructor: %v", err)
	}
	if _, err := NewGetProduct(nil); !errors.Is(err, ErrReaderRequired) {
		t.Errorf("get constructor: %v", err)
	}
}
