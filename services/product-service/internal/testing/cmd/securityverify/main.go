// Command securityverify probes an already running Product target without changing it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

const (
	probeDeadline   = 20 * time.Second
	requestDeadline = 3 * time.Second
	maxBodyBytes    = 1 << 20
)

type options struct {
	context   string
	namespace string
	http      string
	grpc      string
	health    string
	accepted  string
}

func main() {
	var opts options
	flag.StringVar(&opts.context, "context", "", "explicit target context or isolated")
	flag.StringVar(&opts.namespace, "namespace", "", "explicit target namespace or isolated")
	flag.StringVar(&opts.http, "http", "", "Product HTTP base URL")
	flag.StringVar(&opts.grpc, "grpc", "", "Product gRPC host:port")
	flag.StringVar(&opts.health, "health", "", "Product management HTTP base URL")
	flag.StringVar(&opts.accepted, "accepted", "", "accepted direct-access observation file")
	flag.Parse()
	if err := verify(opts); err != nil {
		fmt.Fprintf(os.Stderr, "product-security-verify: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf(
		"product-security-verify: anonymous HTTP/gRPC catalog and safe management probes passed for %s/%s\n",
		opts.context,
		opts.namespace,
	)
}

func verify(opts options) error {
	if opts.context == "" || opts.namespace == "" || opts.accepted == "" {
		return errors.New("context, namespace, and accepted observation are required")
	}
	if err := validateHTTPBase(opts.http); err != nil {
		return errors.New("invalid Product HTTP endpoint")
	}
	if err := validateHTTPBase(opts.health); err != nil {
		return errors.New("invalid management endpoint")
	}
	if err := validateAddress(opts.grpc); err != nil {
		return errors.New("invalid Product gRPC endpoint")
	}
	if opts.http == opts.health || opts.http == "http://"+opts.grpc ||
		opts.health == "http://"+opts.grpc {
		return errors.New("product HTTP, gRPC, and management endpoints must be distinct")
	}
	want, err := acceptedResponses(opts.accepted)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeDeadline)
	defer cancel()
	client := &http.Client{
		Timeout: requestDeadline,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, route := range []struct{ name, path string }{
		{"list", "/products"}, {"get", "/products/TRI"},
	} {
		got, readErr := get(ctx, client, opts.http+route.path)
		if readErr != nil {
			return fmt.Errorf(
				"product HTTP %s: request or bounded read failed",
				route.name,
			)
		}
		if integrationtest.CompareHTTP(want[route.name], got) != nil {
			return fmt.Errorf(
				"product HTTP %s: accepted catalog response mismatch",
				route.name,
			)
		}
	}
	for _, route := range []struct{ name, path, body string }{
		{"liveness", "/livez", "live\n"},
		{"readiness", "/readyz", "ready\n"},
	} {
		got, readErr := get(ctx, client, opts.health+route.path)
		if readErr != nil || got.Status != http.StatusOK ||
			string(got.Body) != route.body ||
			got.ContentType != "text/plain; charset=utf-8" {
			return fmt.Errorf(
				"management %s: safe healthy response mismatch",
				route.name,
			)
		}
	}
	for _, endpoint := range []string{opts.http, opts.health} {
		got, readErr := get(ctx, client, endpoint+"/debug/pprof/")
		if readErr != nil || got.Status != http.StatusNotFound {
			return errors.New("HTTP profiling endpoint is available or unreachable")
		}
	}
	connection, err := grpcgo.NewClient(opts.grpc,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return errors.New("product gRPC endpoint is unreachable")
	}
	defer func() { _ = connection.Close() }()
	service := productv1.NewProductServiceClient(connection)
	listCtx, stopList := context.WithTimeout(ctx, requestDeadline)
	list, err := service.ListProducts(listCtx, &productv1.ListProductsRequest{})
	stopList()
	if err != nil {
		if status.Code(err) == codes.Unavailable ||
			status.Code(err) == codes.DeadlineExceeded {
			return errors.New("product gRPC endpoint is unreachable")
		}
		return errors.New("anonymous Product gRPC list failed")
	}
	getCtx, stopGet := context.WithTimeout(ctx, requestDeadline)
	item, err := service.GetProduct(getCtx, &productv1.GetProductRequest{Code: "TRI"})
	stopGet()
	if err != nil {
		if status.Code(err) == codes.Unavailable ||
			status.Code(err) == codes.DeadlineExceeded {
			return errors.New("product gRPC endpoint is unreachable")
		}
		return errors.New("anonymous Product gRPC get failed")
	}
	for _, route := range []struct {
		name     string
		products []*productv1.Product
	}{
		{"list", list.GetProducts()}, {"get", []*productv1.Product{item.GetProduct()}},
	} {
		expected, parseErr := integrationtest.ParseGRPCExpectations(want[route.name].Body)
		if parseErr != nil || !sameProducts(expected, route.products) {
			return fmt.Errorf(
				"product gRPC %s: accepted catalog response mismatch",
				route.name,
			)
		}
	}
	reflectionCtx, stopReflection := context.WithTimeout(ctx, requestDeadline)
	defer stopReflection()
	stream, err := reflectionv1.NewServerReflectionClient(connection).
		ServerReflectionInfo(reflectionCtx)
	if err == nil {
		err = stream.Send(&reflectionv1.ServerReflectionRequest{
			MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{
				ListServices: "",
			},
		})
		if err == nil {
			_, err = stream.Recv()
		}
	}
	if status.Code(err) != codes.Unimplemented {
		return errors.New("gRPC reflection is available or returned an unexpected result")
	}
	return nil
}

func validateHTTPBase(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid HTTP base URL")
	}
	return validateAddress(parsed.Host)
}

func validateAddress(raw string) error {
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" {
		return errors.New("invalid host and port")
	}
	value, err := strconv.Atoi(port)
	if err != nil || value < 1 || value > 65535 {
		return errors.New("invalid port")
	}
	return nil
}

func get(
	ctx context.Context,
	client *http.Client,
	address string,
) (integrationtest.HTTPExpectation, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return integrationtest.HTTPExpectation{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return integrationtest.HTTPExpectation{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return integrationtest.HTTPExpectation{}, errors.New("response exceeds read limit")
	}
	return integrationtest.HTTPExpectation{
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}, nil
}

func acceptedResponses(path string) (map[string]integrationtest.HTTPExpectation, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("accepted access observation cannot be read")
	}
	var fixture struct {
		Observations []struct {
			ScenarioID string `json:"scenarioId"`
			Boundary   string `json:"boundary"`
			Response   struct {
				Status      int    `json:"status"`
				ContentType string `json:"contentType"`
				RawBody     string `json:"rawBody"`
			} `json:"response"`
		} `json:"observations"`
	}
	if json.Unmarshal(content, &fixture) != nil {
		return nil, errors.New("accepted access observation is invalid")
	}
	want := make(map[string]integrationtest.HTTPExpectation, 2)
	for _, observation := range fixture.Observations {
		for route, id := range map[string]string{
			"list": "ACCESS-DIRECT-LIST-NONE-001", "get": "ACCESS-DIRECT-GET-NONE-001",
		} {
			if observation.ScenarioID != id {
				continue
			}
			if observation.Boundary != "direct" ||
				observation.Response.Status != http.StatusOK ||
				observation.Response.ContentType != "application/json" ||
				observation.Response.RawBody == "" {
				return nil, errors.New(
					"accepted direct access observation has an unexpected shape",
				)
			}
			if _, duplicate := want[route]; duplicate {
				return nil, errors.New(
					"accepted direct access observation is duplicated",
				)
			}
			want[route] = integrationtest.HTTPExpectation{
				Status:      observation.Response.Status,
				ContentType: observation.Response.ContentType,
				Body:        []byte(observation.Response.RawBody),
			}
		}
	}
	if len(want) != 2 {
		return nil, errors.New("accepted direct access observations are incomplete")
	}
	return want, nil
}

func sameProducts(
	expected []integrationtest.GRPCProductExpectation,
	actual []*productv1.Product,
) bool {
	if len(expected) != len(actual) {
		return false
	}
	want := make(map[string]integrationtest.GRPCProductExpectation, len(expected))
	got := make(map[string]integrationtest.GRPCProductExpectation, len(actual))
	for _, item := range expected {
		if _, duplicate := want[item.Code]; duplicate || item.Code == "" {
			return false
		}
		want[item.Code] = item
	}
	for _, item := range actual {
		if item == nil {
			return false
		}
		converted := grpcProduct(item)
		if _, duplicate := got[converted.Code]; duplicate || converted.Code == "" {
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
