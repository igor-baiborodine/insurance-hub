//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

const accessServiceName = "product-access-diagnostics"

func TestProductAccess(t *testing.T) {
	// given
	image := os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE")
	if image != DefaultPostgresImage {
		t.Fatalf("PRODUCT_TEST_POSTGRES_IMAGE does not contain the pinned image")
	}
	if selection := os.Getenv(
		"PRODUCT_INTEGRATION_FIXTURE_SET",
	); selection != string(
		FixtureQA,
	) {
		t.Fatalf("fixture selection = %q, want qa", selection)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// The Product process has no gateway verifier or signing configuration.
	for _, key := range []string{
		"MICRONAUT_SECURITY_TOKEN_JWT_SIGNATURES_SECRET_GENERATOR_SECRET",
		"JWT_SECRET",
	} {
		previous, present := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("remove gateway signing setting %s: %v", key, err)
		}
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(key, previous)
			}
		})
		if _, present := os.LookupEnv(key); present {
			t.Fatalf("gateway signing setting %s remains set", key)
		}
	}
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	if _, err := harness.LoadCatalog(ctx, FixtureQA); err != nil {
		t.Fatalf("load QA catalog: %v", err)
	}

	tokenMarker := generatedMarker(t, "access-token")
	directAuthorizations, directSensitiveMarkers := newDirectAuthorizations(t, tokenMarker)
	catalogMarker := generatedMarker(t, "catalog-definition")
	runtimeURL := harness.RuntimeDatabaseURL()
	runtimePassword := databasePassword(t, runtimeURL)
	addresses := serviceAddresses{
		http:   freeAddress(t),
		grpc:   freeAddress(t),
		health: freeAddress(t),
	}
	collector, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for disabled collector proof: %v", err)
	}
	defer func() { _ = collector.Close() }()
	t.Setenv("SERVICE_NAME", accessServiceName)
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://"+collector.Addr().String())
	settings := loadServiceSettings(t, runtimeURL, addresses)
	if settings.Telemetry.Enabled || settings.Telemetry.ExporterEndpoint == nil {
		t.Fatalf("unexpected disabled telemetry settings: %#v", settings.Telemetry)
	}

	var logs lockedLogBuffer
	serviceCtx, stopService := context.WithCancel(context.Background())
	serviceResult := make(chan error, 1)
	go func() {
		serviceResult <- service.Run(
			serviceCtx,
			settings,
			logger.New(&logs, settings.ServiceName, settings.LogLevel),
		)
	}()
	serviceStopped := false
	t.Cleanup(func() {
		if serviceStopped {
			return
		}
		stopService()
		select {
		case <-serviceResult:
		case <-time.After(5 * time.Second):
			t.Error("Product access service did not stop during cleanup")
		}
	})
	waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)

	connection, err := grpcgo.NewClient(
		addresses.grpc,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("create Product gRPC client: %v", err)
	}
	defer func() { _ = connection.Close() }()
	grpcClient := productv1.NewProductServiceClient(connection)
	directCases := loadDirectAccessCases(t, baselineRoot)
	assertDirectAccessCaseSet(t, directCases)
	captured := loadCapturedDirectAccess(t, baselineRoot)
	grpcCaptured := loadQAGRPCExpectations(t, baselineRoot)
	identityMarker := generatedMarker(t, "access-identity")

	t.Run("accepted HTTP credential matrix remains anonymous", func(t *testing.T) {
		if len(captured) != 8 {
			t.Fatalf(
				"captured direct access fixtures = %d, want 8",
				len(captured),
			)
		}
		for _, accessCase := range directCases {
			t.Run(accessCase.ID, func(t *testing.T) {
				anonymous := executeAccessHTTP(
					t,
					addresses.http,
					accessCase.Path,
					"",
					"",
				)
				actual := executeAccessHTTPWithHeaders(
					t,
					addresses.http,
					accessCase.Path,
					directAuthorizations[accessCase.Credential],
					"",
					accessIdentityHeaders(identityMarker),
				)
				if actual.Status != accessCase.Expected.Status ||
					actual.ContentType != accessCase.Expected.ContentType {
					t.Fatalf(
						"HTTP access response = (%d, %q), want (%d, %q)",
						actual.Status,
						actual.ContentType,
						accessCase.Expected.Status,
						accessCase.Expected.ContentType,
					)
				}
				assertAccessBodyType(t, actual.Body, accessCase.Expected.BodyType)
				if compareErr := CompareHTTP(anonymous, actual); compareErr != nil {
					t.Errorf(
						"HTTP identity metadata changed catalog result: %v",
						compareErr,
					)
				}
				if want, ok := captured[accessCase.ID]; ok {
					if compareErr := CompareHTTP(
						want,
						actual,
					); compareErr != nil {
						t.Errorf(
							"captured direct access parity: %v",
							compareErr,
						)
					}
				}
			})
		}
	})

	t.Run("generated gRPC client ignores identity metadata", func(t *testing.T) {
		for _, accessCase := range directCases {
			t.Run(accessCase.ID, func(t *testing.T) {
				callCtx := outgoingAccessContext(
					ctx,
					directAuthorizations[accessCase.Credential],
					identityMarker,
				)
				if strings.Contains(accessCase.ID, "-LIST-") {
					anonymous, anonymousErr := grpcClient.ListProducts(
						ctx,
						&productv1.ListProductsRequest{},
					)
					response, callErr := grpcClient.ListProducts(
						callCtx,
						&productv1.ListProductsRequest{},
					)
					if anonymousErr != nil || callErr != nil {
						t.Fatalf(
							"gRPC list calls failed: anonymous=%v supplied=%v",
							status.Code(anonymousErr),
							status.Code(callErr),
						)
					}
					if len(response.GetProducts()) != 4 ||
						!proto.Equal(anonymous, response) {
						t.Fatal(
							"gRPC list with identity metadata differs from anonymous catalog",
						)
					}
					assertGRPCProducts(
						t,
						grpcCaptured["HTTP-DIRECT-LIST-001"],
						response.GetProducts(),
					)
					return
				}
				anonymous, anonymousErr := grpcClient.GetProduct(
					ctx,
					&productv1.GetProductRequest{Code: "TRI"},
				)
				response, callErr := grpcClient.GetProduct(
					callCtx,
					&productv1.GetProductRequest{Code: "TRI"},
				)
				if anonymousErr != nil || callErr != nil {
					t.Fatalf(
						"gRPC get calls failed: anonymous=%v supplied=%v",
						status.Code(anonymousErr),
						status.Code(callErr),
					)
				}
				if response.GetProduct().GetCode() != "TRI" ||
					!proto.Equal(anonymous, response) {
					t.Fatal(
						"gRPC get with identity metadata differs from anonymous product",
					)
				}
				assertGRPCProducts(t, grpcCaptured["HTTP-DIRECT-GET-TRI-001"],
					[]*productv1.Product{response.GetProduct()})
			})
		}
	})

	t.Run("application errors ignore identity metadata", func(t *testing.T) {
		failures := loadHTTPFailureExpectations(t, baselineRoot)
		headers := accessIdentityHeaders(identityMarker)
		headers.Set("Authorization", directAuthorizations["valid"])
		for _, caseID := range []string{
			"FAIL-PRODUCT-MISSING-001",
			"FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001",
		} {
			t.Run(caseID, func(t *testing.T) {
				fixture, ok := failures[caseID]
				if !ok {
					t.Fatalf("missing HTTP failure fixture %s", caseID)
				}
				want, err := GoHTTPExpectation(caseID, fixture.Expectation)
				if err != nil {
					t.Fatalf("resolve HTTP failure expectation: %v", err)
				}
				var anonymous, supplied HTTPExpectation
				if caseID == "FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001" {
					anonymous = rawHTTPIntegrationGET(
						t,
						addresses.http,
						fixture.Path,
					)
					supplied = rawHTTPIntegrationGETWithHeaders(
						t, addresses.http, fixture.Path, headers,
					)
				} else {
					anonymous = executeAccessHTTP(
						t,
						addresses.http,
						fixture.Path,
						"",
						"",
					)
					supplied = executeAccessHTTPWithHeaders(
						t,
						addresses.http,
						fixture.Path,
						directAuthorizations["valid"],
						"",
						accessIdentityHeaders(identityMarker),
					)
				}
				for label, actual := range map[string]HTTPExpectation{
					"anonymous": anonymous,
					"supplied":  supplied,
				} {
					if compareErr := CompareHTTP(
						want,
						actual,
					); compareErr != nil {
						t.Errorf(
							"%s HTTP error contract: %v",
							label,
							compareErr,
						)
					}
				}
			})
		}

		missing := failures["FAIL-PRODUCT-MISSING-001"]
		if !strings.HasPrefix(missing.Path, "/products/") {
			t.Fatalf("missing product fixture path = %q", missing.Path)
		}
		missingCode := strings.TrimPrefix(missing.Path, "/products/")
		callCtx := outgoingAccessContext(ctx, directAuthorizations["valid"], identityMarker)
		for _, testCase := range []struct {
			name    string
			request *productv1.GetProductRequest
			code    codes.Code
			message string
		}{
			{
				"missing product", &productv1.GetProductRequest{Code: missingCode},
				codes.NotFound, "product not found",
			},
			{
				"invalid request", &productv1.GetProductRequest{},
				codes.InvalidArgument, "invalid request",
			},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				_, anonymousErr := grpcClient.GetProduct(ctx, testCase.request)
				_, suppliedErr := grpcClient.GetProduct(callCtx, testCase.request)
				assertSafeGRPCFailure(
					t,
					anonymousErr,
					testCase.code,
					testCase.message,
				)
				assertSafeGRPCFailure(
					t,
					suppliedErr,
					testCase.code,
					testCase.message,
				)
			})
		}
	})

	t.Run("reflection and profiling are absent", func(t *testing.T) {
		assertReflectionDisabled(t, ctx, connection)
		for _, endpoint := range []struct {
			address string
			path    string
		}{
			{address: addresses.http, path: "/debug/pprof/"},
			{address: addresses.health, path: "/debug/pprof/"},
		} {
			if got := httpStatus(
				t,
				endpoint.address,
				endpoint.path,
			); got != http.StatusNotFound {
				t.Errorf(
					"GET %s%s status = %d, want 404",
					endpoint.address,
					endpoint.path,
					got,
				)
			}
		}
	})

	t.Run("invalid storage and requests expose generic diagnostics", func(t *testing.T) {
		corruptDefinition := fmt.Sprintf(
			`{"name":%q,"questions":[{"type":"numeric","unexpected":%q}]}`,
			catalogMarker,
			catalogMarker,
		)
		if err := harness.ResetRows(ctx, []FixtureRow{
			{Code: "CORRUPT", RawLosslessDefinitionJSON: corruptDefinition},
		}); err != nil {
			t.Fatalf("reset corrupt catalog: %v", err)
		}
		traceparent := "00-0102030405060708090a0b0c0d0e0f10-1112131415161718-01"
		httpFailure := executeAccessHTTP(
			t,
			addresses.http,
			"/products/CORRUPT",
			directAuthorizations["valid"],
			traceparent,
		)
		assertGenericHTTPFailure(t, httpFailure)
		callCtx := metadata.AppendToOutgoingContext(
			ctx,
			"authorization", directAuthorizations["valid"],
			"traceparent", traceparent,
		)
		_, grpcFailure := grpcClient.GetProduct(
			callCtx,
			&productv1.GetProductRequest{Code: "CORRUPT"},
		)
		assertSafeGRPCFailure(t, grpcFailure, codes.Internal, "internal error")
		_, invalidRequest := grpcClient.GetProduct(ctx, &productv1.GetProductRequest{})
		assertSafeGRPCFailure(t, invalidRequest, codes.InvalidArgument, "invalid request")
		if statusCode, body := httpBody(
			t,
			addresses.health,
			"/readyz",
		); statusCode != http.StatusOK ||
			string(body) != "ready\n" {
			t.Errorf("readiness after corrupt catalog = (%d, %q)", statusCode, body)
		}
	})

	t.Run("dependency loss remains safe and recovers", func(t *testing.T) {
		execAdmin(t, harness, "REVOKE SELECT ON TABLE public.product FROM "+runtimeRole)
		restored := false
		defer func() {
			if !restored {
				execAdmin(
					t,
					harness,
					"GRANT SELECT ON TABLE public.product TO "+runtimeRole,
				)
			}
		}()
		waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusServiceUnavailable)
		failure := executeAccessHTTP(
			t,
			addresses.http,
			"/products",
			directAuthorizations["valid"],
			"",
		)
		assertGenericHTTPFailure(t, failure)
		_, grpcFailure := grpcClient.ListProducts(ctx, &productv1.ListProductsRequest{})
		assertSafeGRPCFailure(t, grpcFailure, codes.Unavailable, "service unavailable")
		if statusCode, body := httpBody(
			t,
			addresses.health,
			"/readyz",
		); statusCode != http.StatusServiceUnavailable ||
			string(body) != "not ready\n" {
			t.Errorf("readiness during dependency loss = (%d, %q)", statusCode, body)
		}
		execAdmin(t, harness, "GRANT SELECT ON TABLE public.product TO "+runtimeRole)
		restored = true
		waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)
	})

	// when
	stopService()
	select {
	case runErr := <-serviceResult:
		if runErr != nil {
			t.Errorf("run Product access service: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Product access service did not stop within the test bound")
	}
	serviceStopped = true
	waitForReaderConnections(t, harness, 0)
	assertNoCollectorConnection(t, collector)

	t.Run("startup failure logging redacts wrapped credentials", func(t *testing.T) {
		badPassword := generatedMarker(t, "database-password")
		badURL := databaseURLWithPassword(t, runtimeURL, badPassword)
		badSettings := loadServiceSettings(t, badURL, serviceAddresses{
			http: freeAddress(t), grpc: freeAddress(t), health: freeAddress(t),
		})
		var startupLogs lockedLogBuffer
		startupLogger := logger.New(&startupLogs, accessServiceName, slog.LevelDebug)
		startupErr := service.Run(context.Background(), badSettings, startupLogger)
		if startupErr == nil {
			t.Fatal("startup with bad reader credential succeeded")
		}
		startupLogger.ErrorContext(context.Background(), "service failed",
			slog.Any("error", fmt.Errorf("startup wrapper: %w", startupErr)),
		)
		assertNoSensitiveText(
			t,
			"startup diagnostics",
			startupErr.Error()+startupLogs.String(),
			badPassword,
			badURL,
			runtimeURL,
		)
	})

	assertStructuredSafeLogs(t, logs.String(), accessServiceName)
	sensitiveMarkers := append([]string{
		identityMarker,
		catalogMarker,
		runtimePassword,
		runtimeURL,
	}, directSensitiveMarkers...)
	assertNoSensitiveText(
		t,
		"service diagnostics",
		logs.String(),
		sensitiveMarkers...,
	)
}

type directAccessCase struct {
	ID         string `json:"id"`
	Target     string `json:"target"`
	Path       string `json:"path"`
	Credential string `json:"credential"`
	Expected   struct {
		Status      int    `json:"status"`
		ContentType string `json:"contentType"`
		BodyType    string `json:"bodyType"`
	} `json:"expected"`
}

func assertDirectAccessCaseSet(t *testing.T, cases []directAccessCase) {
	t.Helper()
	credentials := map[string]string{
		"NONE": "missing", "VALID": "valid", "MALFORMED": "malformed",
		"SIGNATURE": "invalid-signature", "EXPIRED": "expired", "NBF": "not-before",
		"ISSUER": "issuer-variation", "AUDIENCE": "audience-variation",
		"ROLE": "role-variation",
	}
	type expectedCase struct {
		path       string
		credential string
		bodyType   string
	}
	want := make(map[string]expectedCase, 18)
	for _, route := range []struct {
		name, path, bodyType string
	}{
		{"LIST", "/products", "array"},
		{"GET", "/products/TRI", "object"},
	} {
		for suffix, credential := range credentials {
			id := fmt.Sprintf("ACCESS-DIRECT-%s-%s-001", route.name, suffix)
			want[id] = expectedCase{route.path, credential, route.bodyType}
		}
	}
	seen := make(map[string]bool, len(cases))
	for _, accessCase := range cases {
		expected, ok := want[accessCase.ID]
		if !ok || seen[accessCase.ID] {
			t.Fatalf(
				"direct access case set has unknown or duplicate ID %q",
				accessCase.ID,
			)
		}
		seen[accessCase.ID] = true
		if accessCase.Path != expected.path ||
			accessCase.Credential != expected.credential ||
			accessCase.Expected.Status != http.StatusOK ||
			accessCase.Expected.ContentType != "application/json" ||
			accessCase.Expected.BodyType != expected.bodyType {
			t.Fatalf(
				"direct access case %s disagrees with accepted path, credential, or result",
				accessCase.ID,
			)
		}
	}
	if len(seen) != len(want) {
		for id := range want {
			if !seen[id] {
				t.Errorf("direct access case set is missing %s", id)
			}
		}
	}
}

func loadDirectAccessCases(t *testing.T, baselineRoot string) []directAccessCase {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "access", "cases.json"))
	if err != nil {
		t.Fatalf("read access cases: %v", err)
	}
	var fixture struct {
		Cases []directAccessCase `json:"cases"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode access cases: %v", err)
	}
	direct := make([]directAccessCase, 0, len(fixture.Cases))
	for _, accessCase := range fixture.Cases {
		if accessCase.Target == "product" {
			direct = append(direct, accessCase)
		}
	}
	return direct
}

func loadCapturedDirectAccess(
	t *testing.T,
	baselineRoot string,
) map[string]HTTPExpectation {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "access", "qa.json"))
	if err != nil {
		t.Fatalf("read captured access observations: %v", err)
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
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode captured access observations: %v", err)
	}
	result := make(map[string]HTTPExpectation)
	for _, observation := range fixture.Observations {
		if observation.Boundary == "direct" {
			result[observation.ScenarioID] = HTTPExpectation{
				Status:      observation.Response.Status,
				ContentType: observation.Response.ContentType,
				Body:        []byte(observation.Response.RawBody),
			}
		}
	}
	return result
}

func executeAccessHTTP(
	t *testing.T,
	address string,
	path string,
	authorization string,
	traceparent string,
) HTTPExpectation {
	t.Helper()
	return executeAccessHTTPWithHeaders(t, address, path, authorization, traceparent, nil)
}

func executeAccessHTTPWithHeaders(
	t *testing.T,
	address string,
	path string,
	authorization string,
	traceparent string,
	headers http.Header,
) HTTPExpectation {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, "http://"+address+path, nil,
	)
	if err != nil {
		t.Fatalf("create access request: %v", err)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if traceparent != "" {
		request.Header.Set("traceparent", traceparent)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("execute access request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read access response: %v", err)
	}
	return HTTPExpectation{
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}
}

type directJWTClaims struct {
	Subject   string   `json:"sub"`
	Roles     []string `json:"roles"`
	Issuer    string   `json:"iss"`
	Audience  []string `json:"aud"`
	IssuedAt  int64    `json:"iat"`
	NotBefore int64    `json:"nbf"`
	ExpiresAt int64    `json:"exp"`
}

func newDirectAuthorizations(t *testing.T, marker string) (map[string]string, []string) {
	t.Helper()
	signingMaterial, err := randomCredential()
	if err != nil {
		t.Fatalf("generate disposable JWT signing material: %v", err)
	}
	key := []byte(signingMaterial)
	wrongKey := append([]byte(nil), key...)
	wrongKey[0] ^= 1
	now := time.Now().UTC()
	baseline := directJWTClaims{
		Subject:   marker,
		Roles:     []string{"CAR"},
		Issuer:    "auth-service",
		Audience:  []string{"product-service"},
		IssuedAt:  now.Add(-time.Minute).Unix(),
		NotBefore: now.Add(-time.Minute).Unix(),
		ExpiresAt: now.Add(10 * time.Minute).Unix(),
	}
	expired := baseline
	expired.IssuedAt = now.Add(-15 * time.Minute).Unix()
	expired.NotBefore = expired.IssuedAt
	expired.ExpiresAt = now.Add(-5 * time.Minute).Unix()
	future := baseline
	future.NotBefore = now.Add(5 * time.Minute).Unix()
	issuer := baseline
	issuer.Issuer = "different-issuer"
	audience := baseline
	audience.Audience = []string{"different-audience"}
	role := baseline
	role.Roles = []string{"UNRELATED"}
	values := map[string]string{
		"missing":            "",
		"malformed":          "Bearer not-a-jwt-" + marker,
		"valid":              "Bearer " + signDirectJWT(t, key, baseline),
		"invalid-signature":  "Bearer " + signDirectJWT(t, wrongKey, baseline),
		"expired":            "Bearer " + signDirectJWT(t, key, expired),
		"not-before":         "Bearer " + signDirectJWT(t, key, future),
		"issuer-variation":   "Bearer " + signDirectJWT(t, key, issuer),
		"audience-variation": "Bearer " + signDirectJWT(t, key, audience),
		"role-variation":     "Bearer " + signDirectJWT(t, key, role),
	}
	sensitive := []string{marker, signingMaterial, string(wrongKey)}
	for _, value := range values {
		if value != "" {
			sensitive = append(sensitive, strings.TrimPrefix(value, "Bearer "))
		}
	}
	return values, sensitive
}

func signDirectJWT(t *testing.T, key []byte, claims directJWTClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("encode disposable JWT claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	unsigned := header + "." + encodedPayload
	signature := hmac.New(sha256.New, key)
	_, _ = signature.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature.Sum(nil))
}

func accessIdentityHeaders(marker string) http.Header {
	values := make(http.Header)
	values.Set("X-User", marker)
	values.Set("X-Principal", marker)
	values.Set("X-Role", "UNRELATED")
	values.Set("X-Authentication", marker)
	values.Set("X-Forwarded-User", marker)
	return values
}

func outgoingAccessContext(ctx context.Context, authorization, marker string) context.Context {
	values := []string{
		"x-user", marker,
		"x-principal", marker,
		"x-role", "UNRELATED",
		"x-authentication", marker,
		"x-forwarded-user", marker,
	}
	if authorization != "" {
		values = append(values, "authorization", authorization)
	}
	return metadata.AppendToOutgoingContext(ctx, values...)
}

func assertAccessBodyType(t *testing.T, body []byte, want string) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode access response: %v", err)
	}
	switch want {
	case "array":
		if _, ok := decoded.([]any); !ok {
			t.Fatalf("access body type = %T, want array", decoded)
		}
	case "object":
		if _, ok := decoded.(map[string]any); !ok {
			t.Fatalf("access body type = %T, want object", decoded)
		}
	default:
		t.Fatalf("unsupported accepted access body type: %q", want)
	}
}

func assertReflectionDisabled(t *testing.T, ctx context.Context, connection *grpcgo.ClientConn) {
	t.Helper()
	stream, err := reflectionv1alpha.NewServerReflectionClient(connection).
		ServerReflectionInfo(ctx)
	if err != nil {
		if status.Code(err) != codes.Unimplemented {
			t.Fatalf("open reflection stream: %v", err)
		}
		return
	}
	err = stream.Send(&reflectionv1alpha.ServerReflectionRequest{
		MessageRequest: &reflectionv1alpha.ServerReflectionRequest_ListServices{
			ListServices: "",
		},
	})
	if err == nil {
		_, err = stream.Recv()
	}
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("reflection status = %v, want Unimplemented: %v", status.Code(err), err)
	}
}

func assertGenericHTTPFailure(t *testing.T, actual HTTPExpectation) {
	t.Helper()
	if actual.Status != http.StatusInternalServerError ||
		actual.ContentType != "application/json" ||
		string(actual.Body) != `{"message":"Internal Server Error"}` {
		t.Fatal("HTTP failure did not match the generic error contract")
	}
}

func assertSafeGRPCFailure(t *testing.T, err error, wantCode codes.Code, wantMessage string) {
	t.Helper()
	if status.Code(err) != wantCode || status.Convert(err).Message() != wantMessage {
		t.Fatalf("gRPC failure did not match code %v and the safe message", wantCode)
	}
}

func generatedMarker(t *testing.T, prefix string) string {
	t.Helper()
	value, err := randomCredential()
	if err != nil {
		t.Fatalf("generate %s marker: %v", prefix, err)
	}
	return prefix + "-" + value
}

func databasePassword(t *testing.T, databaseURL string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse runtime URL: %v", err)
	}
	password, ok := parsed.User.Password()
	if !ok || password == "" {
		t.Fatal("runtime URL does not contain the generated reader password")
	}
	return password
}

func assertNoCollectorConnection(t *testing.T, collector net.Listener) {
	t.Helper()
	tcpListener, ok := collector.(*net.TCPListener)
	if !ok {
		t.Fatalf("collector listener type = %T, want TCP", collector)
	}
	if err := tcpListener.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set collector deadline: %v", err)
	}
	connection, err := collector.Accept()
	if err == nil {
		_ = connection.Close()
		t.Fatal("disabled telemetry connected to the configured collector")
	}
	var networkErr net.Error
	if !errors.As(err, &networkErr) || !networkErr.Timeout() {
		t.Fatalf("accept disabled collector connection: %v", err)
	}
}

func assertStructuredSafeLogs(t *testing.T, content, serviceName string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("service emitted no structured diagnostics")
	}
	started := false
	failedRequest := false
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode structured log entry: %v", err)
		}
		if entry["service"] != serviceName || entry["level"] == nil || entry["msg"] == nil {
			t.Fatal("log lacks service/level/message context")
		}
		switch entry["msg"] {
		case "product service started":
			started = true
		case "Product request completed":
			failedRequest = entry["outcome"] == "storage_unavailable" ||
				entry["outcome"] == "invalid_data" || entry["outcome"] == "internal_error" ||
				failedRequest
		}
	}
	if !started || !failedRequest {
		t.Fatal("expected startup and request diagnostics were missing")
	}
}

func assertNoSensitiveText(t *testing.T, label, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if marker != "" && strings.Contains(text, marker) {
			t.Fatalf("%s exposed a generated sensitive marker", label)
		}
	}
}

type lockedLogBuffer struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedLogBuffer) Write(content []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.buffer.Write(content)
}

func (buffer *lockedLogBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.buffer.String()
}
