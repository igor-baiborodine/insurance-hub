package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

func TestValidateTarget(t *testing.T) {
	valid := options{
		context: "qa-insurance-hub", namespace: "qa-svc",
		address: "127.0.0.1:19083", fixture: "accepted.json",
	}
	if err := validate(valid); err != nil {
		t.Fatalf("valid target: %v", err)
	}
	for _, change := range []func(*options){
		func(o *options) { o.context = "local-dev" },
		func(o *options) { o.namespace = "default" },
		func(o *options) { o.address = "qa-product-api.qa-svc:9090" },
		func(o *options) { o.address = "127.0.0.1:0" },
		func(o *options) { o.fixture = "" },
	} {
		candidate := valid
		change(&candidate)
		if validate(candidate) == nil {
			t.Fatalf("invalid target accepted: %+v", candidate)
		}
	}
}

type fakeClient struct {
	calls         []string
	wrongNegative bool
}

func (client *fakeClient) ListProducts(_ context.Context, _ *productv1.ListProductsRequest,
	_ ...grpc.CallOption,
) (*productv1.ListProductsResponse, error) {
	client.calls = append(client.calls, "list")
	return &productv1.ListProductsResponse{Products: []*productv1.Product{
		{Code: "CAR"}, {Code: "FAI"}, {Code: "HSI"}, {Code: "TRI"},
	}}, nil
}

func (client *fakeClient) GetProduct(_ context.Context, request *productv1.GetProductRequest,
	_ ...grpc.CallOption,
) (*productv1.GetProductResponse, error) {
	client.calls = append(client.calls, request.GetCode())
	for _, code := range catalogCodes {
		if request.GetCode() == code {
			return &productv1.GetProductResponse{
				Product: &productv1.Product{Code: code},
			}, nil
		}
	}
	if client.wrongNegative {
		return nil, status.Error(codes.Internal, "synthetic failure")
	}
	if request.GetCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "synthetic invalid")
	}
	return nil, status.Error(codes.NotFound, "synthetic missing")
}

func TestClientChecksAllSelectedCases(t *testing.T) {
	want := map[string][]integrationtest.GRPCProductExpectation{
		"list": {{Code: "CAR"}, {Code: "FAI"}, {Code: "HSI"}, {Code: "TRI"}},
	}
	for _, code := range catalogCodes {
		want[code] = []integrationtest.GRPCProductExpectation{{Code: code}}
	}
	client := &fakeClient{}
	if err := checkClient(context.Background(), client, want); err != nil {
		t.Fatalf("selected cases: %v", err)
	}
	if !reflect.DeepEqual(client.calls, []string{
		"list", "CAR", "FAI", "HSI", "TRI", "MISSING", "car", "",
	}) {
		t.Fatalf("unexpected call set: %v", client.calls)
	}
	client = &fakeClient{wrongNegative: true}
	if err := checkClient(context.Background(), client, want); err == nil {
		t.Fatal("incorrect negative status was accepted")
	}
}

func TestGRPCDecimalPresenceAndScaleAreCompared(t *testing.T) {
	sum := "200000"
	want := []integrationtest.GRPCProductExpectation{{
		Code: "FAI",
		Covers: []integrationtest.GRPCCoverExpectation{{
			Code: "C1", SumInsured: &sum,
		}},
	}}
	actual := []*productv1.Product{{
		Code:   "FAI",
		Covers: []*productv1.Cover{{Code: "C1", SumInsured: &sum}},
	}}
	if !sameProducts(want, actual) {
		t.Fatal("matching decimal rejected")
	}
	actual[0].Covers[0].SumInsured = nil
	if sameProducts(want, actual) {
		t.Fatal("missing optional decimal was accepted")
	}
	scaled := "200000.0"
	actual[0].Covers[0].SumInsured = &scaled
	if sameProducts(want, actual) {
		t.Fatal("changed decimal scale was accepted")
	}
}

func TestAcceptedFixtureAndSemanticDrift(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "..", "..", "legacy", "product-service",
		"src", "test", "resources", "product-read-baseline", "http", "qa.json")
	want, err := expectations(path)
	if err != nil {
		t.Fatalf("accepted fixture: %v", err)
	}
	if len(want) != 5 || len(want["list"]) != 4 || !consistentExpectations(want) {
		t.Fatal("accepted fixture is incomplete")
	}
	changed := make(map[string][]integrationtest.GRPCProductExpectation, len(want))
	for key, value := range want {
		changed[key] = append([]integrationtest.GRPCProductExpectation(nil), value...)
	}
	changed["CAR"] = append(changed["CAR"], changed["CAR"][0])
	if consistentExpectations(changed) {
		t.Fatal("duplicate accepted get was accepted")
	}
	if sameProducts(want["list"], []*productv1.Product{{Code: "CAR"}}) {
		t.Fatal("incomplete live catalog was accepted")
	}
	fixture := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(fixture, []byte(`{"environment":"local-dev"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := expectations(fixture); err == nil {
		t.Fatal("wrong environment was accepted")
	}
}
