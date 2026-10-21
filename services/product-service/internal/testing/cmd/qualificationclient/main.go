// Command qualificationclient checks the accepted QA Product gRPC reads through a local forward.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"reflect"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

var catalogCodes = []string{"CAR", "FAI", "HSI", "TRI"}

type options struct {
	context, namespace, address, fixture string
}

func main() {
	var opts options
	flag.StringVar(&opts.context, "context", "", "Kubernetes context")
	flag.StringVar(&opts.namespace, "namespace", "", "Kubernetes namespace")
	flag.StringVar(&opts.address, "address", "", "localhost forwarded gRPC host:port")
	flag.StringVar(&opts.fixture, "fixture", "", "accepted QA HTTP fixture")
	flag.Parse()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, "product-qualification-grpc: check failed:", err)
		os.Exit(1)
	}
	fmt.Println("product-qualification-grpc: QA list, four gets, and negative cases passed")
}

func validate(opts options) error {
	if opts.context != "qa-insurance-hub" || opts.namespace != "qa-svc" || opts.fixture == "" {
		return errors.New(
			"explicit QA context, namespace, and accepted fixture are required",
		)
	}
	host, port, err := net.SplitHostPort(opts.address)
	number, parseErr := strconv.Atoi(port)
	if err != nil || parseErr != nil || number < 1 || number > 65535 ||
		(host != "localhost" && host != "127.0.0.1") {
		return errors.New("gRPC address must be a localhost port-forward")
	}
	return nil
}

func expectations(path string) (map[string][]integrationtest.GRPCProductExpectation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("accepted fixture cannot be read")
	}
	var fixture struct {
		Environment  string `json:"environment"`
		Observations []struct {
			ScenarioID string `json:"scenarioId"`
			Response   struct {
				Status  int    `json:"status"`
				RawBody string `json:"rawBody"`
			} `json:"response"`
		} `json:"observations"`
	}
	if json.Unmarshal(content, &fixture) != nil || fixture.Environment != "qa" {
		return nil, errors.New("accepted fixture is not valid QA JSON")
	}
	want := make(map[string][]integrationtest.GRPCProductExpectation, 5)
	for _, observation := range fixture.Observations {
		key := ""
		if observation.ScenarioID == "HTTP-DIRECT-LIST-001" {
			key = "list"
		}
		for _, code := range catalogCodes {
			if observation.ScenarioID == "HTTP-DIRECT-GET-"+code+"-001" {
				key = code
			}
		}
		if key == "" {
			continue
		}
		if observation.Response.Status != 200 || observation.Response.RawBody == "" {
			return nil, errors.New("accepted direct observation is invalid")
		}
		if _, exists := want[key]; exists {
			return nil, errors.New("accepted direct observation is duplicated")
		}
		parsed, parseErr := integrationtest.ParseGRPCExpectations(
			[]byte(observation.Response.RawBody),
		)
		if parseErr != nil {
			return nil, errors.New("accepted direct Product body is invalid")
		}
		want[key] = parsed
	}
	if len(want) != 5 || len(want["list"]) != 4 || !consistentExpectations(want) {
		return nil, errors.New("accepted direct catalog is incomplete or inconsistent")
	}
	return want, nil
}

// consistentExpectations checks that each accepted get equals its unique list entry.
func consistentExpectations(want map[string][]integrationtest.GRPCProductExpectation) bool {
	seen := make(map[string]bool, len(want["list"]))
	for _, item := range want["list"] {
		if item.Code == "" || seen[item.Code] {
			return false
		}
		seen[item.Code] = true
	}
	for _, code := range catalogCodes {
		if len(want[code]) != 1 || want[code][0].Code != code {
			return false
		}
		found := false
		for _, item := range want["list"] {
			if item.Code == code && reflect.DeepEqual(item, want[code][0]) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return len(seen) == len(catalogCodes)
}

func run(opts options) error {
	if err := validate(opts); err != nil {
		return err
	}
	want, err := expectations(opts.fixture)
	if err != nil {
		return err
	}
	connection, err := grpc.NewClient(
		opts.address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return errors.New("gRPC connection could not be created")
	}
	defer func() { _ = connection.Close() }()
	client := productv1.NewProductServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return checkClient(ctx, client, want)
}

func checkClient(
	ctx context.Context,
	client productv1.ProductServiceClient,
	want map[string][]integrationtest.GRPCProductExpectation,
) error {
	callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	list, err := client.ListProducts(callCtx, &productv1.ListProductsRequest{})
	stop()
	if err != nil || !sameProducts(want["list"], list.GetProducts()) {
		return errors.New("gRPC list differs from accepted QA catalog")
	}
	for _, code := range catalogCodes {
		callCtx, stop = context.WithTimeout(ctx, 5*time.Second)
		item, callErr := client.GetProduct(
			callCtx,
			&productv1.GetProductRequest{Code: code},
		)
		stop()
		if callErr != nil ||
			!sameProducts(want[code], []*productv1.Product{item.GetProduct()}) {
			return fmt.Errorf("gRPC get %s differs from accepted QA catalog", code)
		}
	}
	for _, test := range []struct {
		code string
		want codes.Code
	}{{"MISSING", codes.NotFound}, {"car", codes.NotFound}, {"", codes.InvalidArgument}} {
		callCtx, stop = context.WithTimeout(ctx, 5*time.Second)
		_, callErr := client.GetProduct(
			callCtx,
			&productv1.GetProductRequest{Code: test.code},
		)
		stop()
		if status.Code(callErr) != test.want {
			return fmt.Errorf(
				"gRPC negative get %q returned %s",
				test.code,
				status.Code(callErr),
			)
		}
	}
	return nil
}

func sameProducts(
	expected []integrationtest.GRPCProductExpectation,
	actual []*productv1.Product,
) bool {
	if len(expected) != len(actual) || len(expected) == 0 {
		return false
	}
	want := make(map[string]integrationtest.GRPCProductExpectation, len(expected))
	got := make(map[string]integrationtest.GRPCProductExpectation, len(actual))
	for _, item := range expected {
		if item.Code == "" {
			return false
		}
		if _, duplicate := want[item.Code]; duplicate {
			return false
		}
		want[item.Code] = item
	}
	for _, item := range actual {
		if item == nil {
			return false
		}
		converted := grpcProduct(item)
		if converted.Code == "" {
			return false
		}
		if _, duplicate := got[converted.Code]; duplicate {
			return false
		}
		got[converted.Code] = converted
	}
	return reflect.DeepEqual(want, got)
}

func grpcProduct(item *productv1.Product) integrationtest.GRPCProductExpectation {
	result := integrationtest.GRPCProductExpectation{
		Code: item.GetCode(), Name: item.GetName(), Image: item.GetImage(),
		Description: item.GetDescription(), Icon: item.GetIcon(),
		MaxNumberOfInsured: item.GetMaxNumberOfInsured(),
	}
	for _, cover := range item.GetCovers() {
		converted := integrationtest.GRPCCoverExpectation{
			Code:        cover.GetCode(),
			Name:        cover.GetName(),
			Description: cover.GetDescription(),
			Optional:    cover.GetOptional(),
		}
		if cover.SumInsured != nil {
			value := cover.GetSumInsured()
			converted.SumInsured = &value
		}
		result.Covers = append(result.Covers, converted)
	}
	for _, question := range item.GetQuestions() {
		converted := integrationtest.GRPCQuestionExpectation{
			Code:  question.GetCode(),
			Text:  question.GetText(),
			Index: question.GetIndex(),
		}
		switch {
		case question.GetChoice() != nil:
			converted.Kind = "choice"
			for _, choice := range question.GetChoice().GetChoices() {
				converted.Choices = append(
					converted.Choices,
					integrationtest.GRPCChoiceExpectation{
						Code: choice.GetCode(), Label: choice.GetLabel(),
					},
				)
			}
		case question.GetDate() != nil:
			converted.Kind = "date"
		case question.GetNumeric() != nil:
			converted.Kind = "numeric"
		}
		result.Questions = append(result.Questions, converted)
	}
	return result
}
