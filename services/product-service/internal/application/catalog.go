// Package application owns Product catalog reads and their reader port.
package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

var (
	ErrReaderRequired    = errors.New("product reader is required")
	ErrCodeRequired      = errors.New("product code is required")
	ErrProductNotFound   = errors.New("product not found")
	ErrInvalidDefinition = errors.New("invalid product definition")
	ErrUnavailable       = errors.New("product reader unavailable")
)

// ProductReader returns complete catalog results. A missing get returns found=false
// without an error. Implementations classify corrupt rows and unavailable storage
// with ErrInvalidDefinition and ErrUnavailable while preserving their causes.
type ProductReader interface {
	ListProducts(context.Context) ([]domain.Product, error)
	GetProduct(context.Context, string) (product domain.Product, found bool, err error)
}

type ListProducts struct{ reader ProductReader }

func NewListProducts(reader ProductReader) (ListProducts, error) {
	if reader == nil {
		return ListProducts{}, ErrReaderRequired
	}
	return ListProducts{reader: reader}, nil
}

func (useCase ListProducts) Execute(ctx context.Context) ([]domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	products, err := useCase.reader.ListProducts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if products == nil {
		return []domain.Product{}, nil
	}
	return products, nil
}

type GetProduct struct{ reader ProductReader }

func NewGetProduct(reader ProductReader) (GetProduct, error) {
	if reader == nil {
		return GetProduct{}, ErrReaderRequired
	}
	return GetProduct{reader: reader}, nil
}

func (useCase GetProduct) Execute(ctx context.Context, code string) (domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	if code == "" {
		return domain.Product{}, ErrCodeRequired
	}
	product, found, err := useCase.reader.GetProduct(ctx, code)
	if err != nil {
		return domain.Product{}, fmt.Errorf("get product: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	if !found {
		return domain.Product{}, ErrProductNotFound
	}
	return product, nil
}
