package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	checkedInFixture    = "../../internal/chat/testdata/java-parity.json"
	checkedInProvenance = "../../internal/chat/testdata/java-parity-provenance.json"
)

func TestValidateFiles_AcceptsCheckedInOracle(t *testing.T) {
	if err := validateFiles(checkedInFixture, checkedInProvenance); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFiles_RejectsInvalidFiles(t *testing.T) {
	fixtureBytes := mustRead(t, checkedInFixture)
	provenanceBytes := mustRead(t, checkedInProvenance)

	tests := []struct {
		name            string
		fixtureBytes    []byte
		provenanceBytes []byte
		want            string
	}{
		{
			name:            "invalid fixture JSON",
			fixtureBytes:    []byte("{"),
			provenanceBytes: provenanceBytes,
			want:            "decode fixture",
		},
		{
			name:            "invalid fixture",
			fixtureBytes:    []byte(`{"schemaVersion": 2}`),
			provenanceBytes: provenanceBytes,
			want:            "fixture schema version",
		},
		{
			name:            "invalid provenance JSON",
			fixtureBytes:    fixtureBytes,
			provenanceBytes: []byte("{"),
			want:            "decode provenance",
		},
		{
			name:            "invalid provenance",
			fixtureBytes:    fixtureBytes,
			provenanceBytes: []byte(`{"schemaVersion": 2}`),
			want:            "provenance schema version",
		},
		{
			name:            "fixture digest mismatch",
			fixtureBytes:    append(fixtureBytes, '\n'),
			provenanceBytes: provenanceBytes,
			want:            "fixture SHA-256 does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			fixturePath := filepath.Join(directory, "fixture.json")
			provenancePath := filepath.Join(directory, "provenance.json")
			mustWrite(t, fixturePath, test.fixtureBytes)
			mustWrite(t, provenancePath, test.provenanceBytes)

			err := validateFiles(fixturePath, provenancePath)
			assertErrorContains(t, err, test.want)
		})
	}

	t.Run("missing fixture", func(t *testing.T) {
		err := validateFiles(
			filepath.Join(t.TempDir(), "missing.json"),
			checkedInProvenance,
		)
		assertErrorContains(t, err, "read fixture")
	})
	t.Run("missing provenance", func(t *testing.T) {
		directory := t.TempDir()
		fixturePath := filepath.Join(directory, "fixture.json")
		mustWrite(t, fixturePath, fixtureBytes)
		err := validateFiles(fixturePath, filepath.Join(directory, "missing.json"))
		assertErrorContains(t, err, "read provenance")
	})
}

func TestValidateFixture_RejectsContractDrift(t *testing.T) {
	stringPointer := func(value string) *string { return &value }
	tests := []struct {
		name   string
		mutate func(*fixture)
		want   string
	}{
		{
			name:   "schema",
			mutate: func(value *fixture) { value.SchemaVersion++ },
			want:   "fixture schema version",
		},
		{
			name:   "session count",
			mutate: func(value *fixture) { value.Sessions = value.Sessions[:2] },
			want:   "sessions, want 3",
		},
		{
			name:   "incomplete session",
			mutate: func(value *fixture) { value.Sessions[0].Topic = "" },
			want:   "incomplete session",
		},
		{
			name:   "duplicate session",
			mutate: func(value *fixture) { value.Sessions[1].ID = value.Sessions[0].ID },
			want:   "duplicate session ID",
		},
		{
			name:   "case count",
			mutate: func(value *fixture) { value.Cases = value.Cases[:3] },
			want:   "cases, want 4",
		},
		{
			name:   "case order",
			mutate: func(value *fixture) { value.Cases[0].ID = "OTHER" },
			want:   "case 0 has ID",
		},
		{
			name:   "unknown action session",
			mutate: func(value *fixture) { value.Cases[0].Action.SessionID = "unknown" },
			want:   "unknown action session",
		},
		{
			name:   "missing action payload",
			mutate: func(value *fixture) { value.Cases[1].Action.PayloadUTF8 = nil },
			want:   "missing payload bytes",
		},
		{
			name:   "invalid action base64",
			mutate: func(value *fixture) { value.Cases[1].Action.PayloadBase64 = stringPointer("!") },
			want:   "invalid action payload base64",
		},
		{
			name:   "action representation mismatch",
			mutate: func(value *fixture) { value.Cases[1].Action.PayloadUTF8 = stringPointer("different") },
			want:   "action payload representations differ",
		},
		{
			name:   "payload on open",
			mutate: func(value *fixture) { value.Cases[0].Action.PayloadUTF8 = stringPointer("") },
			want:   "non-message action contains payload",
		},
		{
			name:   "unsupported action",
			mutate: func(value *fixture) { value.Cases[0].Action.Kind = "binary" },
			want:   "unsupported action",
		},
		{
			name:   "delivery count",
			mutate: func(value *fixture) { value.Cases[0].Deliveries = nil },
			want:   "deliveries, want 1",
		},
		{
			name:   "unknown recipient",
			mutate: func(value *fixture) { value.Cases[0].Deliveries[0].RecipientSessionID = "unknown" },
			want:   "unknown recipient",
		},
		{
			name:   "message type",
			mutate: func(value *fixture) { value.Cases[0].Deliveries[0].MessageType = "binary" },
			want:   "want text",
		},
		{
			name:   "invalid delivery base64",
			mutate: func(value *fixture) { value.Cases[0].Deliveries[0].PayloadBase64 = "!" },
			want:   "invalid payload base64",
		},
		{
			name:   "delivery representation mismatch",
			mutate: func(value *fixture) { value.Cases[0].Deliveries[0].PayloadUTF8 = "different" },
			want:   "payload representations differ",
		},
		{
			name:   "delivery digest",
			mutate: func(value *fixture) { value.Cases[0].Deliveries[0].PayloadSHA256 = strings.Repeat("0", 64) },
			want:   "payload SHA-256 differs",
		},
		{
			name:   "unknown silent session",
			mutate: func(value *fixture) { value.Cases[0].SilentSessionIDs[0] = "unknown" },
			want:   "unknown silent session",
		},
		{name: "recipient marked silent", mutate: func(value *fixture) {
			value.Cases[0].SilentSessionIDs[0] = value.Cases[0].Deliveries[0].RecipientSessionID
		}, want: "marks recipient"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := loadFixture(t)
			test.mutate(&value)
			assertErrorContains(t, validateFixture(value), test.want)
		})
	}
}

func TestValidateProvenance_RejectsInvalidIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*provenance)
		want   string
	}{
		{
			name:   "schema",
			mutate: func(value *provenance) { value.SchemaVersion++ },
			want:   "provenance schema version",
		},
		{
			name:   "required identity",
			mutate: func(value *provenance) { value.SourcePath = "" },
			want:   "missing a required identity",
		},
		{
			name:   "coordinate",
			mutate: func(value *provenance) { value.BuildCoordinate = "other" },
			want:   "build coordinate",
		},
		{
			name:   "client",
			mutate: func(value *provenance) { value.Client = "other" },
			want:   "client",
		},
		{
			name:   "capture time",
			mutate: func(value *provenance) { value.CapturedAtUTC = "yesterday" },
			want:   "invalid capture UTC time",
		},
		{
			name:   "source digest",
			mutate: func(value *provenance) { value.SourceSHA256 = "invalid" },
			want:   "invalid source SHA-256",
		},
		{
			name:   "build digest",
			mutate: func(value *provenance) { value.BuildArtifactSHA256 = "invalid" },
			want:   "invalid build SHA-256",
		},
		{
			name:   "fixture digest",
			mutate: func(value *provenance) { value.FixtureSHA256 = "invalid" },
			want:   "invalid fixture SHA-256",
		},
	}

	if err := validateProvenance(loadProvenance(t)); err != nil {
		t.Fatalf("checked-in provenance: %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := loadProvenance(t)
			test.mutate(&value)
			assertErrorContains(t, validateProvenance(value), test.want)
		})
	}
}

func TestCapture_RejectsInvalidInputsBeforeConnecting(t *testing.T) {
	tests := []struct {
		name string
		opts options
		want string
	}{
		{name: "missing identity", opts: options{}, want: "source revision"},
		{
			name: "invalid durations",
			opts: options{
				sourceRevision: "revision",
				sourceFile:     "source",
				buildArtifact:  "artifact",
				captureCommand: "capture",
				timeout:        0,
				quietWindow:    time.Second,
			},
			want: "must be positive",
		},
		{name: "missing source", opts: validCaptureOptions(t), want: "hash source"},
	}
	tests[2].opts.sourceFile = filepath.Join(t.TempDir(), "missing-source")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertErrorContains(t, capture(test.opts), test.want)
		})
	}

	t.Run("missing build artifact", func(t *testing.T) {
		opts := validCaptureOptions(t)
		opts.buildArtifact = filepath.Join(t.TempDir(), "missing-artifact")
		assertErrorContains(t, capture(opts), "hash build artifact")
	})
	t.Run("unavailable endpoint", func(t *testing.T) {
		opts := validCaptureOptions(t)
		assertErrorContains(t, capture(opts), "dial alice-main")
	})
}

func TestVerifyLive_RejectsInvalidTimingBeforeConnecting(t *testing.T) {
	t.Parallel()

	err := verifyLive(options{
		fixturePath:    checkedInFixture,
		provenancePath: checkedInProvenance,
		timeout:        0,
		quietWindow:    defaultQuietWindow,
	})
	assertErrorContains(t, err, "timeout and quiet window must be positive")
}

func TestVerifyLiveWith_ComparesCapturedBehaviorToCheckedInFixture(t *testing.T) {
	t.Parallel()

	fixtureBytes, err := os.ReadFile(checkedInFixture)
	if err != nil {
		t.Fatalf("ReadFile(fixture) error = %v", err)
	}
	var expected fixture
	if err := decodeJSON(fixtureBytes, &expected); err != nil {
		t.Fatalf("decodeJSON(fixture) error = %v", err)
	}
	opts := options{
		fixturePath:    checkedInFixture,
		provenancePath: checkedInProvenance,
		timeout:        defaultTimeout,
		quietWindow:    defaultQuietWindow,
	}

	if err := verifyLiveWith(opts, func(options) (fixture, error) {
		return expected, nil
	}); err != nil {
		t.Fatalf("verifyLiveWith(equal) error = %v", err)
	}

	drifted := expected
	drifted.Oracle = "changed"
	assertErrorContains(t, verifyLiveWith(opts, func(options) (fixture, error) {
		return drifted, nil
	}), "differs from the Java fixture")
	assertErrorContains(t, verifyLiveWith(opts, func(options) (fixture, error) {
		return fixture{}, errors.New("controlled capture failure")
	}), "controlled capture failure")
}

func TestCompareFixture_RejectsLiveContractDrift(t *testing.T) {
	t.Parallel()

	expected := fixture{
		SchemaVersion: fixtureSchemaVersion,
		Oracle:        "legacy Java ChatWebSocket",
	}
	if err := compareFixture(expected, expected); err != nil {
		t.Fatalf("compareFixture(equal) error = %v", err)
	}

	actual := expected
	actual.Oracle = "changed"
	assertErrorContains(t, compareFixture(actual, expected), "differs from the Java fixture")
}

func TestDialClient_ReportsHTTPFailure(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			http.Error(writer, "unavailable", http.StatusServiceUnavailable)
		}),
	)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	_, err := dialClient(endpoint, session{ID: "alice", Topic: "main", Username: "alice"})
	assertErrorContains(t, err, "HTTP 503")
}

func TestDialClient_ReadsTextAndReportsServerClose(t *testing.T) {
	handlerErrors := make(chan error, 1)
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
			if err != nil {
				handlerErrors <- err
				return
			}
			defer func() { _ = connection.Close() }()
			if err := connection.WriteMessage(
				websocket.TextMessage,
				[]byte("hello"),
			); err != nil {
				handlerErrors <- err
			}
		}),
	)
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	actual, err := dialClient(
		endpoint,
		session{ID: "alice", Topic: "main", Username: "alice"},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer actual.close()

	event, err := nextEvent(actual, time.Second)
	if err != nil || string(event.payload) != "hello" {
		t.Fatalf("event = %#v, err = %v", event, err)
	}
	select {
	case err := <-handlerErrors:
		t.Fatalf("server handler: %v", err)
	case err := <-actual.errors:
		if err == nil {
			t.Fatal("server close reported a nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("server close was not reported")
	}
}

func TestCaptureFixture_RejectsUnexpectedSetupDelivery(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer func() { _ = connection.Close() }()
			_ = connection.WriteMessage(websocket.TextMessage, []byte("unexpected"))
		}),
	)
	defer server.Close()

	_, err := captureFixture(options{
		endpoint:    "ws" + strings.TrimPrefix(server.URL, "http"),
		timeout:     time.Second,
		quietWindow: time.Second,
	})
	assertErrorContains(t, err, "alice setup")
}

func TestEventHelpers(t *testing.T) {
	t.Run("next text event", func(t *testing.T) {
		client := channelClient("alice")
		client.events <- receivedEvent{messageType: websocket.TextMessage, payload: []byte("hello")}
		event, err := nextEvent(client, time.Second)
		if err != nil || string(event.payload) != "hello" {
			t.Fatalf("event = %#v, err = %v", event, err)
		}
	})
	t.Run("next binary event", func(t *testing.T) {
		client := channelClient("alice")
		client.events <- receivedEvent{messageType: websocket.BinaryMessage}
		_, err := nextEvent(client, time.Second)
		assertErrorContains(t, err, "want text")
	})
	t.Run("next read error", func(t *testing.T) {
		client := channelClient("alice")
		client.errors <- errors.New("closed")
		_, err := nextEvent(client, time.Second)
		assertErrorContains(t, err, "read failed")
	})
	t.Run("next timeout", func(t *testing.T) {
		_, err := nextEvent(channelClient("alice"), time.Millisecond)
		assertErrorContains(t, err, "received no message")
	})
	t.Run("quiet", func(t *testing.T) {
		if err := assertQuiet(time.Millisecond, channelClient("alice")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unexpected event", func(t *testing.T) {
		client := channelClient("alice")
		client.events <- receivedEvent{messageType: websocket.TextMessage, payload: []byte("hello")}
		assertErrorContains(t, assertQuiet(time.Second, client), "unexpectedly received")
	})
	t.Run("error while quiet", func(t *testing.T) {
		client := channelClient("alice")
		client.errors <- errors.New("closed")
		assertErrorContains(t, assertQuiet(time.Second, client), "checking silence")
	})
}

func TestFixtureConstructionHelpers(t *testing.T) {
	payload := []byte("hello")
	action := newMessageAction("bob", payload)
	if action.PayloadBase64 == nil || *action.PayloadBase64 != "aGVsbG8=" ||
		action.PayloadUTF8 == nil || *action.PayloadUTF8 != "hello" {
		t.Fatalf("action = %#v", action)
	}

	event := receivedEvent{messageType: websocket.TextMessage, payload: payload}
	delivery := newDelivery("alice", event)
	wantDigest := sha256.Sum256(payload)
	if delivery.PayloadBase64 != "aGVsbG8=" ||
		delivery.PayloadSHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("delivery = %#v", delivery)
	}

	value := newCase("case", action, "alice", event, []string{"bob"})
	if value.ID != "case" || len(value.Deliveries) != 1 || value.Deliveries[0] != delivery {
		t.Fatalf("case = %#v", value)
	}
}

func TestJSONAndFileHelpers(t *testing.T) {
	value := fixture{SchemaVersion: fixtureSchemaVersion}
	data, err := marshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("marshaled JSON lacks final newline: %q", data)
	}
	if _, err := marshalJSON(make(chan int)); err == nil {
		t.Fatal("unsupported value marshaled without error")
	}

	var decoded fixture
	if err := decodeJSON(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte(`{"schemaVersion":1,"unknown":true}`),
		append(data, []byte(`{}`)...),
		[]byte("{"),
	} {
		if err := decodeJSON(invalid, &decoded); err == nil {
			t.Fatalf("invalid JSON decoded: %q", invalid)
		}
	}

	path := filepath.Join(t.TempDir(), "nested", "fixture.json")
	if err := writeFile(path, data); err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := sha256.Sum256(data)
	if digest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("digest = %s", digest)
	}
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file hashed without error")
	}
	blockingParent := filepath.Join(t.TempDir(), "file")
	mustWrite(t, blockingParent, []byte("not a directory"))
	if err := writeFile(filepath.Join(blockingParent, "fixture.json"), data); err == nil {
		t.Fatal("nested output beneath a regular file was written without error")
	}
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	var value fixture
	if err := decodeJSON(mustRead(t, checkedInFixture), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func loadProvenance(t *testing.T) provenance {
	t.Helper()
	var value provenance
	if err := decodeJSON(mustRead(t, checkedInProvenance), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func validCaptureOptions(t *testing.T) options {
	t.Helper()
	directory := t.TempDir()
	sourceFile := filepath.Join(directory, "ChatWebSocket.java")
	buildArtifact := filepath.Join(directory, "chat-service.jar")
	mustWrite(t, sourceFile, []byte("source"))
	mustWrite(t, buildArtifact, []byte("artifact"))
	return options{
		endpoint:       "ws://127.0.0.1:1",
		sourceRevision: "revision",
		sourceFile:     sourceFile,
		buildArtifact:  buildArtifact,
		fixturePath:    filepath.Join(directory, "fixture.json"),
		provenancePath: filepath.Join(directory, "provenance.json"),
		captureCommand: "capture",
		timeout:        time.Millisecond,
		quietWindow:    time.Millisecond,
	}
}

func channelClient(id string) *client {
	return &client{
		id:     id,
		events: make(chan receivedEvent, 1),
		errors: make(chan error, 1),
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}
