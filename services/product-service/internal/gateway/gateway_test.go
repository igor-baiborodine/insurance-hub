package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

type (
	ListProducts func(context.Context) ([]domain.Product, error)
	GetProduct   func(context.Context, string) (domain.Product, error)
)

type callbackClient struct {
	list ListProducts
	get  GetProduct
}

func TestGeneratedHandler_StripsApplicationMetadataAndCarriesDeadline(t *testing.T) {
	client := &capturingClient{called: make(chan context.Context, 1)}
	handler, err := NewHandler(client, Settings{RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/products", nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Grpc-Metadata-Identity", "untrusted")
	request.Header.Set("X-User", "untrusted")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "[]" {
		t.Fatalf("response = (%d, %s)", response.Code, response.Body.String())
	}
	callCtx := <-client.called
	if _, ok := callCtx.Deadline(); !ok {
		t.Fatal("gateway call has no request deadline")
	}
	values, _ := metadata.FromOutgoingContext(callCtx)
	for _, key := range []string{"authorization", "identity", "x-user"} {
		if len(values.Get(key)) != 0 {
			t.Fatalf("forwarded metadata %q: %v", key, values.Get(key))
		}
	}
}

func TestClearApplicationMetadata_DropsGatewayValues(t *testing.T) {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"authorization", "secret", "identity", "untrusted",
	))
	invoked := false
	err := ClearApplicationMetadata(
		ctx, "/product.v1.ProductService/ListProducts", nil, nil, nil,
		func(callCtx context.Context, _ string, _, _ any, _ *grpc.ClientConn,
			_ ...grpc.CallOption,
		) error {
			invoked = true
			values, _ := metadata.FromOutgoingContext(callCtx)
			if len(values) != 0 {
				t.Fatalf("metadata survived interceptor: %v", values)
			}
			return nil
		},
	)
	if err != nil || !invoked {
		t.Fatalf("interceptor error = %v, invoked = %t", err, invoked)
	}
}

type capturingClient struct{ called chan context.Context }

func (client *capturingClient) ListProducts(
	ctx context.Context,
	_ *productv1.ListProductsRequest,
	_ ...grpc.CallOption,
) (*productv1.ListProductsResponse, error) {
	client.called <- ctx
	return &productv1.ListProductsResponse{Products: []*productv1.Product{}}, nil
}

func (*capturingClient) GetProduct(
	context.Context,
	*productv1.GetProductRequest,
	...grpc.CallOption,
) (*productv1.GetProductResponse, error) {
	return nil, status.Error(codes.Unimplemented, "unused")
}

func (client callbackClient) ListProducts(
	ctx context.Context,
	_ *productv1.ListProductsRequest,
	_ ...grpc.CallOption,
) (*productv1.ListProductsResponse, error) {
	products, err := client.list(ctx)
	if err != nil {
		return nil, testTransportError(ctx, err)
	}
	response := &productv1.ListProductsResponse{
		Products: make([]*productv1.Product, 0, len(products)),
	}
	for _, product := range products {
		mapped, mapErr := testProtoProduct(product)
		if mapErr != nil {
			return nil, mapErr
		}
		response.Products = append(response.Products, mapped)
	}
	return response, nil
}

func (client callbackClient) GetProduct(
	ctx context.Context,
	request *productv1.GetProductRequest,
	_ ...grpc.CallOption,
) (*productv1.GetProductResponse, error) {
	product, err := client.get(ctx, request.GetCode())
	if err != nil {
		return nil, testTransportError(ctx, err)
	}
	mapped, err := testProtoProduct(product)
	if err != nil {
		return nil, err
	}
	return &productv1.GetProductResponse{Product: mapped}, nil
}

func newCallbackHandler(
	_ *testing.T,
	_ *slog.Logger,
	settings Settings,
	list ListProducts,
	get GetProduct,
) (http.Handler, error) {
	if list == nil || get == nil {
		return nil, errors.New("test callbacks are required")
	}
	return NewHandler(callbackClient{list: list, get: get}, settings)
}

func testTransportError(ctx context.Context, err error) error {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return status.Error(codes.Canceled, "canceled")
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline")
	case errors.Is(err, application.ErrProductNotFound):
		return status.Error(codes.NotFound, "product not found")
	case errors.Is(err, application.ErrUnavailable):
		return status.Error(codes.Unavailable, "service unavailable")
	default:
		return status.Error(codes.Internal, "internal error")
	}
}

func testProtoProduct(product domain.Product) (*productv1.Product, error) {
	if product.Code == "" {
		return nil, status.Error(codes.Internal, "internal error")
	}
	mapped := &productv1.Product{
		Code: product.Code, Name: product.Name, Image: product.Image,
		Description: product.Description, MaxNumberOfInsured: product.MaxNumberOfInsured,
		Icon: product.Icon,
	}
	for _, cover := range product.Covers {
		mappedCover := &productv1.Cover{
			Code: cover.Code, Name: cover.Name, Description: cover.Description,
			Optional: cover.Optional,
		}
		if cover.SumInsured != nil {
			decimal := cover.SumInsured.String()
			mappedCover.SumInsured = &decimal
		}
		mapped.Covers = append(mapped.Covers, mappedCover)
	}
	for _, question := range product.Questions {
		mappedQuestion := &productv1.Question{
			Code: question.Code, Index: question.Index, Text: question.Text,
		}
		switch kind := question.Kind().(type) {
		case domain.ChoiceQuestion:
			choice := &productv1.ChoiceQuestion{}
			for _, item := range kind.Choices {
				choice.Choices = append(choice.Choices, &productv1.Choice{
					Code: item.Code, Label: item.Label,
				})
			}
			mappedQuestion.Kind = &productv1.Question_Choice{Choice: choice}
		case domain.DateQuestion:
			mappedQuestion.Kind = &productv1.Question_Date{
				Date: &productv1.DateQuestion{},
			}
		case domain.NumericQuestion:
			mappedQuestion.Kind = &productv1.Question_Numeric{
				Numeric: &productv1.NumericQuestion{},
			}
		default:
			return nil, status.Error(codes.Internal, "internal error")
		}
		mapped.Questions = append(mapped.Questions, mappedQuestion)
	}
	return mapped, nil
}
