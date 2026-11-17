package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	fixtureSchemaVersion    = 1
	provenanceSchemaVersion = 1
	clientIdentity          = "github.com/gorilla/websocket v1.5.3"
	buildCoordinate         = "pl.altkom.asc.lab.micronaut.poc:chat-service:1.0.0-SNAPSHOT"
	defaultTimeout          = 3 * time.Second
	defaultQuietWindow      = 250 * time.Millisecond
)

var stableCaseIDs = []string{
	"CHAT-JOIN-01",
	"CHAT-MESSAGE-01",
	"CHAT-EMPTY-MESSAGE-01",
	"CHAT-CLOSE-01",
}

type fixture struct {
	SchemaVersion        int          `json:"schemaVersion"`
	Oracle               string       `json:"oracle"`
	SetupSilenceVerified []string     `json:"setupSilenceVerified"`
	Sessions             []session    `json:"sessions"`
	Cases                []parityCase `json:"cases"`
}

type session struct {
	ID       string `json:"id"`
	Topic    string `json:"topic"`
	Username string `json:"username"`
}

type parityCase struct {
	ID               string     `json:"id"`
	Action           action     `json:"action"`
	Deliveries       []delivery `json:"deliveries"`
	SilentSessionIDs []string   `json:"silentSessionIds"`
}

type action struct {
	Kind          string  `json:"kind"`
	SessionID     string  `json:"sessionId"`
	PayloadBase64 *string `json:"payloadBase64,omitempty"`
	PayloadUTF8   *string `json:"payloadUtf8,omitempty"`
}

type delivery struct {
	RecipientSessionID string `json:"recipientSessionId"`
	MessageType        string `json:"messageType"`
	PayloadBase64      string `json:"payloadBase64"`
	PayloadUTF8        string `json:"payloadUtf8"`
	PayloadSHA256      string `json:"payloadSha256"`
}

type provenance struct {
	SchemaVersion            int    `json:"schemaVersion"`
	CapturedAtUTC            string `json:"capturedAtUtc"`
	SourceRepositoryRevision string `json:"sourceRepositoryRevision"`
	SourcePath               string `json:"sourcePath"`
	SourceSHA256             string `json:"sourceSha256"`
	BuildCoordinate          string `json:"buildCoordinate"`
	BuildArtifactPath        string `json:"buildArtifactPath"`
	BuildArtifactSHA256      string `json:"buildArtifactSha256"`
	Endpoint                 string `json:"endpoint"`
	Client                   string `json:"client"`
	CaptureCommand           string `json:"captureCommand"`
	FixturePath              string `json:"fixturePath"`
	FixtureSHA256            string `json:"fixtureSha256"`
}

type options struct {
	mode           string
	endpoint       string
	sourceRevision string
	sourceFile     string
	buildArtifact  string
	fixturePath    string
	provenancePath string
	captureCommand string
	timeout        time.Duration
	quietWindow    time.Duration
}

type receivedEvent struct {
	messageType int
	payload     []byte
}

type client struct {
	id     string
	conn   *websocket.Conn
	events chan receivedEvent
	errors chan error
}

func main() {
	opts := parseOptions()
	var err error
	switch opts.mode {
	case "capture":
		err = capture(opts)
	case "validate":
		err = validateFiles(opts.fixturePath, opts.provenancePath)
	default:
		err = fmt.Errorf("unsupported mode %q", opts.mode)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "java parity %s failed: %v\n", opts.mode, err)
		os.Exit(1)
	}
	fmt.Printf(
		"java parity %s passed: fixture=%s provenance=%s\n",
		opts.mode,
		opts.fixturePath,
		opts.provenancePath,
	)
}

func parseOptions() options {
	var opts options
	flag.StringVar(&opts.mode, "mode", "validate", "capture or validate")
	flag.StringVar(
		&opts.endpoint,
		"endpoint",
		"ws://localhost:8080",
		"Java Chat WebSocket base endpoint",
	)
	flag.StringVar(&opts.sourceRevision, "source-revision", "", "accepted repository revision")
	flag.StringVar(&opts.sourceFile, "source-file", "", "Java ChatWebSocket source path")
	flag.StringVar(&opts.buildArtifact, "build-artifact", "", "Java Chat build artifact path")
	flag.StringVar(
		&opts.fixturePath,
		"fixture",
		"internal/chat/testdata/java-parity.json",
		"fixture output path",
	)
	flag.StringVar(
		&opts.provenancePath,
		"provenance",
		"internal/chat/testdata/java-parity-provenance.json",
		"provenance output path",
	)
	flag.StringVar(&opts.captureCommand, "capture-command", "", "exact owning capture command")
	flag.DurationVar(&opts.timeout, "timeout", defaultTimeout, "expected delivery deadline")
	flag.DurationVar(
		&opts.quietWindow,
		"quiet-window",
		defaultQuietWindow,
		"unexpected delivery observation window",
	)
	flag.Parse()
	return opts
}

func capture(opts options) error {
	if opts.sourceRevision == "" || opts.sourceFile == "" || opts.buildArtifact == "" ||
		opts.captureCommand == "" {
		return errors.New(
			"source revision, source file, build artifact, and capture command are required",
		)
	}
	if opts.timeout <= 0 || opts.quietWindow <= 0 {
		return errors.New("timeout and quiet window must be positive")
	}

	sourceSHA, err := fileSHA256(opts.sourceFile)
	if err != nil {
		return fmt.Errorf("hash source: %w", err)
	}
	buildSHA, err := fileSHA256(opts.buildArtifact)
	if err != nil {
		return fmt.Errorf("hash build artifact: %w", err)
	}

	result, err := captureFixture(opts)
	if err != nil {
		return err
	}
	fixtureBytes, err := marshalJSON(result)
	if err != nil {
		return fmt.Errorf("marshal fixture: %w", err)
	}
	if err := writeFile(opts.fixturePath, fixtureBytes); err != nil {
		return fmt.Errorf("write fixture: %w", err)
	}

	fixtureSum := sha256.Sum256(fixtureBytes)
	record := provenance{
		SchemaVersion:            provenanceSchemaVersion,
		CapturedAtUTC:            time.Now().UTC().Format(time.RFC3339),
		SourceRepositoryRevision: opts.sourceRevision,
		SourcePath:               filepath.ToSlash(opts.sourceFile),
		SourceSHA256:             sourceSHA,
		BuildCoordinate:          buildCoordinate,
		BuildArtifactPath:        filepath.ToSlash(opts.buildArtifact),
		BuildArtifactSHA256:      buildSHA,
		Endpoint:                 opts.endpoint,
		Client:                   clientIdentity,
		CaptureCommand:           opts.captureCommand,
		FixturePath:              filepath.ToSlash(opts.fixturePath),
		FixtureSHA256:            hex.EncodeToString(fixtureSum[:]),
	}
	provenanceBytes, err := marshalJSON(record)
	if err != nil {
		return fmt.Errorf("marshal provenance: %w", err)
	}
	if err := writeFile(opts.provenancePath, provenanceBytes); err != nil {
		return fmt.Errorf("write provenance: %w", err)
	}
	return validateFiles(opts.fixturePath, opts.provenancePath)
}

func captureFixture(opts options) (fixture, error) {
	result := fixture{
		SchemaVersion:        fixtureSchemaVersion,
		Oracle:               "legacy Java ChatWebSocket",
		SetupSilenceVerified: []string{"alice-main-open", "carol-other-open"},
		Sessions: []session{
			{ID: "alice-main", Topic: "main", Username: "alice"},
			{ID: "bob-main", Topic: "Main", Username: "bob"},
			{ID: "carol-other", Topic: "other", Username: "carol"},
		},
	}

	alice, err := dialClient(opts.endpoint, result.Sessions[0])
	if err != nil {
		return fixture{}, err
	}
	defer alice.close()
	if err := assertQuiet(opts.quietWindow, alice); err != nil {
		return fixture{}, fmt.Errorf("alice setup: %w", err)
	}

	carol, err := dialClient(opts.endpoint, result.Sessions[2])
	if err != nil {
		return fixture{}, err
	}
	defer carol.close()
	if err := assertQuiet(opts.quietWindow, alice, carol); err != nil {
		return fixture{}, fmt.Errorf("other-topic setup: %w", err)
	}

	bob, err := dialClient(opts.endpoint, result.Sessions[1])
	if err != nil {
		return fixture{}, err
	}
	defer bob.close()
	joinEvent, err := nextEvent(alice, opts.timeout)
	if err != nil {
		return fixture{}, fmt.Errorf("join delivery: %w", err)
	}
	if err := assertQuiet(opts.quietWindow, alice, bob, carol); err != nil {
		return fixture{}, fmt.Errorf("join isolation: %w", err)
	}
	result.Cases = append(result.Cases, newCase(
		"CHAT-JOIN-01",
		action{Kind: "open", SessionID: bob.id},
		alice.id,
		joinEvent,
		[]string{bob.id, carol.id},
	))

	inertHTML := []byte("<span data-chat-parity=\"true\">hello</span>")
	if err := bob.conn.WriteMessage(websocket.TextMessage, inertHTML); err != nil {
		return fixture{}, fmt.Errorf("send inert HTML: %w", err)
	}
	messageEvent, err := nextEvent(alice, opts.timeout)
	if err != nil {
		return fixture{}, fmt.Errorf("message delivery: %w", err)
	}
	if err := assertQuiet(opts.quietWindow, alice, bob, carol); err != nil {
		return fixture{}, fmt.Errorf("message isolation: %w", err)
	}
	result.Cases = append(result.Cases, newCase(
		"CHAT-MESSAGE-01",
		newMessageAction(bob.id, inertHTML),
		alice.id,
		messageEvent,
		[]string{bob.id, carol.id},
	))

	if err := bob.conn.WriteMessage(websocket.TextMessage, nil); err != nil {
		return fixture{}, fmt.Errorf("send empty text: %w", err)
	}
	emptyEvent, err := nextEvent(alice, opts.timeout)
	if err != nil {
		return fixture{}, fmt.Errorf("empty message delivery: %w", err)
	}
	if err := assertQuiet(opts.quietWindow, alice, bob, carol); err != nil {
		return fixture{}, fmt.Errorf("empty message isolation: %w", err)
	}
	result.Cases = append(result.Cases, newCase(
		"CHAT-EMPTY-MESSAGE-01",
		newMessageAction(bob.id, nil),
		alice.id,
		emptyEvent,
		[]string{bob.id, carol.id},
	))

	deadline := time.Now().Add(opts.timeout)
	if err := bob.conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		deadline,
	); err != nil {
		return fixture{}, fmt.Errorf("close bob: %w", err)
	}
	closeEvent, err := nextEvent(alice, opts.timeout)
	if err != nil {
		return fixture{}, fmt.Errorf("close delivery: %w", err)
	}
	if err := assertQuiet(opts.quietWindow, alice, carol); err != nil {
		return fixture{}, fmt.Errorf("close isolation: %w", err)
	}
	result.Cases = append(result.Cases, newCase(
		"CHAT-CLOSE-01",
		action{Kind: "normal-close", SessionID: bob.id},
		alice.id,
		closeEvent,
		[]string{carol.id},
	))

	return result, nil
}

func dialClient(endpoint string, session session) (*client, error) {
	address := strings.TrimRight(endpoint, "/") + "/ws/chat/" +
		url.PathEscape(session.Topic) + "/" + url.PathEscape(session.Username)
	conn, response, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf(
				"dial %s: HTTP %d: %w",
				session.ID,
				response.StatusCode,
				err,
			)
		}
		return nil, fmt.Errorf("dial %s: %w", session.ID, err)
	}
	c := &client{
		id:     session.ID,
		conn:   conn,
		events: make(chan receivedEvent, 16),
		errors: make(chan error, 1),
	}
	go c.read()
	return c, nil
}

func (c *client) read() {
	for {
		messageType, payload, err := c.conn.ReadMessage()
		if err != nil {
			select {
			case c.errors <- err:
			default:
			}
			return
		}
		c.events <- receivedEvent{messageType: messageType, payload: bytes.Clone(payload)}
	}
}

func (c *client) close() {
	_ = c.conn.Close()
}

func nextEvent(c *client, timeout time.Duration) (receivedEvent, error) {
	select {
	case event := <-c.events:
		if event.messageType != websocket.TextMessage {
			return receivedEvent{}, fmt.Errorf(
				"%s received message type %d, want text",
				c.id,
				event.messageType,
			)
		}
		return event, nil
	case err := <-c.errors:
		return receivedEvent{}, fmt.Errorf("%s read failed: %w", c.id, err)
	case <-time.After(timeout):
		return receivedEvent{}, fmt.Errorf(
			"%s received no message within %s",
			c.id,
			timeout,
		)
	}
}

func assertQuiet(window time.Duration, clients ...*client) error {
	for _, c := range clients {
		select {
		case event := <-c.events:
			return fmt.Errorf(
				"%s unexpectedly received message type %d with %d payload bytes",
				c.id,
				event.messageType,
				len(event.payload),
			)
		case err := <-c.errors:
			return fmt.Errorf("%s read failed while checking silence: %w", c.id, err)
		case <-time.After(window):
		}
	}
	return nil
}

func newCase(
	id string,
	action action,
	recipient string,
	event receivedEvent,
	silent []string,
) parityCase {
	return parityCase{
		ID:               id,
		Action:           action,
		Deliveries:       []delivery{newDelivery(recipient, event)},
		SilentSessionIDs: silent,
	}
}

func newMessageAction(sessionID string, payload []byte) action {
	payloadBase64 := base64.StdEncoding.EncodeToString(payload)
	payloadUTF8 := string(payload)
	return action{
		Kind:          "text-message",
		SessionID:     sessionID,
		PayloadBase64: &payloadBase64,
		PayloadUTF8:   &payloadUTF8,
	}
}

func newDelivery(recipient string, event receivedEvent) delivery {
	sum := sha256.Sum256(event.payload)
	return delivery{
		RecipientSessionID: recipient,
		MessageType:        "text",
		PayloadBase64:      base64.StdEncoding.EncodeToString(event.payload),
		PayloadUTF8:        string(event.payload),
		PayloadSHA256:      hex.EncodeToString(sum[:]),
	}
}

func validateFiles(fixturePath string, provenancePath string) error {
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		return fmt.Errorf("read fixture: %w", err)
	}
	var actualFixture fixture
	if err := decodeJSON(fixtureBytes, &actualFixture); err != nil {
		return fmt.Errorf("decode fixture: %w", err)
	}
	if err := validateFixture(actualFixture); err != nil {
		return err
	}

	provenanceBytes, err := os.ReadFile(provenancePath)
	if err != nil {
		return fmt.Errorf("read provenance: %w", err)
	}
	var actualProvenance provenance
	if err := decodeJSON(provenanceBytes, &actualProvenance); err != nil {
		return fmt.Errorf("decode provenance: %w", err)
	}
	if err := validateProvenance(actualProvenance); err != nil {
		return err
	}
	fixtureSum := sha256.Sum256(fixtureBytes)
	if actualProvenance.FixtureSHA256 != hex.EncodeToString(fixtureSum[:]) {
		return errors.New("fixture SHA-256 does not match provenance")
	}
	return nil
}

func validateFixture(actual fixture) error {
	if actual.SchemaVersion != fixtureSchemaVersion {
		return fmt.Errorf(
			"fixture schema version %d, want %d",
			actual.SchemaVersion,
			fixtureSchemaVersion,
		)
	}
	if len(actual.Sessions) != 3 {
		return fmt.Errorf("fixture has %d sessions, want 3", len(actual.Sessions))
	}
	sessionIDs := make(map[string]struct{}, len(actual.Sessions))
	for _, session := range actual.Sessions {
		if session.ID == "" || session.Topic == "" || session.Username == "" {
			return errors.New("fixture contains an incomplete session")
		}
		if _, duplicate := sessionIDs[session.ID]; duplicate {
			return fmt.Errorf("duplicate session ID %q", session.ID)
		}
		sessionIDs[session.ID] = struct{}{}
	}
	if len(actual.Cases) != len(stableCaseIDs) {
		return fmt.Errorf(
			"fixture has %d cases, want %d",
			len(actual.Cases),
			len(stableCaseIDs),
		)
	}
	for index, parityCase := range actual.Cases {
		if parityCase.ID != stableCaseIDs[index] {
			return fmt.Errorf(
				"case %d has ID %q, want %q",
				index,
				parityCase.ID,
				stableCaseIDs[index],
			)
		}
		if _, ok := sessionIDs[parityCase.Action.SessionID]; !ok {
			return fmt.Errorf(
				"case %s uses unknown action session %q",
				parityCase.ID,
				parityCase.Action.SessionID,
			)
		}
		switch parityCase.Action.Kind {
		case "text-message":
			if parityCase.Action.PayloadBase64 == nil ||
				parityCase.Action.PayloadUTF8 == nil {
				return fmt.Errorf(
					"case %s text action is missing payload bytes",
					parityCase.ID,
				)
			}
			payload, err := base64.StdEncoding.DecodeString(
				*parityCase.Action.PayloadBase64,
			)
			if err != nil {
				return fmt.Errorf(
					"case %s has invalid action payload base64: %w",
					parityCase.ID,
					err,
				)
			}
			if !bytes.Equal(payload, []byte(*parityCase.Action.PayloadUTF8)) {
				return fmt.Errorf(
					"case %s action payload representations differ",
					parityCase.ID,
				)
			}
		case "open", "normal-close":
			if parityCase.Action.PayloadBase64 != nil ||
				parityCase.Action.PayloadUTF8 != nil {
				return fmt.Errorf(
					"case %s non-message action contains payload bytes",
					parityCase.ID,
				)
			}
		default:
			return fmt.Errorf(
				"case %s has unsupported action %q",
				parityCase.ID,
				parityCase.Action.Kind,
			)
		}
		if len(parityCase.Deliveries) != 1 {
			return fmt.Errorf(
				"case %s has %d deliveries, want 1",
				parityCase.ID,
				len(parityCase.Deliveries),
			)
		}
		recipients := make(map[string]struct{}, len(parityCase.Deliveries))
		for _, delivery := range parityCase.Deliveries {
			if _, ok := sessionIDs[delivery.RecipientSessionID]; !ok {
				return fmt.Errorf(
					"case %s uses unknown recipient %q",
					parityCase.ID,
					delivery.RecipientSessionID,
				)
			}
			if delivery.MessageType != "text" {
				return fmt.Errorf(
					"case %s has message type %q, want text",
					parityCase.ID,
					delivery.MessageType,
				)
			}
			payload, err := base64.StdEncoding.DecodeString(delivery.PayloadBase64)
			if err != nil {
				return fmt.Errorf(
					"case %s has invalid payload base64: %w",
					parityCase.ID,
					err,
				)
			}
			if !bytes.Equal(payload, []byte(delivery.PayloadUTF8)) {
				return fmt.Errorf(
					"case %s payload representations differ",
					parityCase.ID,
				)
			}
			sum := sha256.Sum256(payload)
			if delivery.PayloadSHA256 != hex.EncodeToString(sum[:]) {
				return fmt.Errorf("case %s payload SHA-256 differs", parityCase.ID)
			}
			recipients[delivery.RecipientSessionID] = struct{}{}
		}
		for _, silentID := range parityCase.SilentSessionIDs {
			if _, ok := sessionIDs[silentID]; !ok {
				return fmt.Errorf(
					"case %s uses unknown silent session %q",
					parityCase.ID,
					silentID,
				)
			}
			if _, delivered := recipients[silentID]; delivered {
				return fmt.Errorf(
					"case %s marks recipient %q silent",
					parityCase.ID,
					silentID,
				)
			}
		}
	}
	return nil
}

func validateProvenance(actual provenance) error {
	if actual.SchemaVersion != provenanceSchemaVersion {
		return fmt.Errorf(
			"provenance schema version %d, want %d",
			actual.SchemaVersion,
			provenanceSchemaVersion,
		)
	}
	if actual.SourceRepositoryRevision == "" || actual.SourcePath == "" ||
		actual.BuildArtifactPath == "" ||
		actual.CaptureCommand == "" {
		return errors.New("provenance is missing a required identity")
	}
	if actual.BuildCoordinate != buildCoordinate {
		return fmt.Errorf(
			"build coordinate %q, want %q",
			actual.BuildCoordinate,
			buildCoordinate,
		)
	}
	if actual.Client != clientIdentity {
		return fmt.Errorf("client %q, want %q", actual.Client, clientIdentity)
	}
	if _, err := time.Parse(time.RFC3339, actual.CapturedAtUTC); err != nil {
		return fmt.Errorf("invalid capture UTC time: %w", err)
	}
	for label, value := range map[string]string{
		"source":  actual.SourceSHA256,
		"build":   actual.BuildArtifactSHA256,
		"fixture": actual.FixtureSHA256,
	} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("invalid %s SHA-256", label)
		}
	}
	return nil
}

func decodeJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
