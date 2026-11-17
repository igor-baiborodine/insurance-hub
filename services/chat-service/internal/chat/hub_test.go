package chat_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
)

type parityFixture struct {
	SetupSilenceVerified []string         `json:"setupSilenceVerified"`
	Sessions             []fixtureSession `json:"sessions"`
	Cases                []fixtureCase    `json:"cases"`
}

type fixtureSession struct {
	ID       string `json:"id"`
	Topic    string `json:"topic"`
	Username string `json:"username"`
}

type fixtureCase struct {
	ID               string            `json:"id"`
	Action           fixtureAction     `json:"action"`
	Deliveries       []fixtureDelivery `json:"deliveries"`
	SilentSessionIDs []string          `json:"silentSessionIds"`
}

type fixtureAction struct {
	Kind          string  `json:"kind"`
	SessionID     string  `json:"sessionId"`
	PayloadBase64 *string `json:"payloadBase64,omitempty"`
}

type fixtureDelivery struct {
	RecipientSessionID string `json:"recipientSessionId"`
	MessageType        string `json:"messageType"`
	PayloadBase64      string `json:"payloadBase64"`
}

func TestHub_JavaParityFixture(t *testing.T) {
	// given
	fixture := loadParityFixture(t)
	hub := newHub(t, 8)
	sessionSpecs := make(map[string]fixtureSession, len(fixture.Sessions))
	for _, session := range fixture.Sessions {
		sessionSpecs[session.ID] = session
	}
	sessions := make(map[string]*chat.Session, len(fixture.Sessions))
	for _, setupID := range fixture.SetupSilenceVerified {
		fixtureID := strings.TrimSuffix(setupID, "-open")
		if fixtureID == setupID {
			t.Fatalf("setup ID %q is missing -open suffix", setupID)
		}
		sessions[fixtureID] = joinFixtureSession(t, hub, sessionSpecs[fixtureID])
		assertSilent(t, sessions)
	}

	for _, parityCase := range fixture.Cases {
		t.Run(parityCase.ID, func(t *testing.T) {
			// when
			applyFixtureAction(t, hub, sessions, sessionSpecs, parityCase.Action)

			// then
			assertFixtureDeliveries(t, sessions, parityCase.Deliveries)
			assertSilentIDs(t, sessions, parityCase.SilentSessionIDs)
			assertNoDuplicateDeliveries(t, sessions, parityCase.Deliveries)
		})
	}
}

func TestHub_BroadcastFreezesPayload(t *testing.T) {
	// given
	hub := newHub(t, 8)
	sender := join(t, hub, "main", "alice")
	recipient := join(t, hub, "MAIN", "bob")
	assertMessage(t, sender, []byte("<p>[bob] Joined!</p>"))
	payload := []byte("original")

	// when
	if err := hub.Broadcast(sender, payload); err != nil {
		t.Fatal(err)
	}
	copy(payload, "mutated!")

	// then
	assertMessage(t, recipient, []byte("original"))
	assertNoMessage(t, sender)
}

func TestHub_QueueSaturationEvictsOnlySlowSession(t *testing.T) {
	// given
	hub := newHub(t, 2)
	sender := join(t, hub, "main", "sender")
	slow := join(t, hub, "MAIN", "slow")
	assertMessage(t, sender, []byte("<p>[slow] Joined!</p>"))
	healthy := join(t, hub, "main", "healthy")
	assertMessage(t, sender, []byte("<p>[healthy] Joined!</p>"))
	assertMessage(t, slow, []byte("<p>[healthy] Joined!</p>"))
	for _, payload := range [][]byte{[]byte("one"), []byte("two")} {
		if err := hub.Broadcast(sender, payload); err != nil {
			t.Fatal(err)
		}
		assertMessage(t, healthy, payload)
	}

	// when
	err := hub.Broadcast(sender, []byte("three"))
	// then
	if err != nil {
		t.Fatal(err)
	}
	assertDone(t, slow, chat.RemovalQueueFull)
	assertMessage(t, healthy, []byte("three"))
	assertMessage(t, healthy, []byte("<p>[slow] Disconnected!</p>"))
	assertMessage(t, sender, []byte("<p>[slow] Disconnected!</p>"))
	assertNoMessage(t, sender)
	assertNoMessage(t, healthy)
	assertActive(t, sender)
	assertActive(t, healthy)
	if hub.Leave(slow) {
		t.Fatal("saturated session removed twice")
	}
}

func TestHub_BeginDrainSuppressesDisconnectAndRejectsJoin(t *testing.T) {
	// given
	hub := newHub(t, 8)
	alice := join(t, hub, "main", "alice")
	bob := join(t, hub, "Main", "bob")
	assertMessage(t, alice, []byte("<p>[bob] Joined!</p>"))

	// when
	hub.BeginDrain()
	hub.BeginDrain()

	// then
	assertDone(t, alice, chat.RemovalDraining)
	assertDone(t, bob, chat.RemovalDraining)
	assertNoMessage(t, alice)
	assertNoMessage(t, bob)
	if hub.Leave(alice) || hub.Leave(bob) {
		t.Fatal("drained session removed twice")
	}
	if _, err := hub.Join("main", "carol"); !errors.Is(err, chat.ErrDraining) {
		t.Fatalf("join error = %v, want %v", err, chat.ErrDraining)
	}
}

func TestHub_ConcurrentJoinBroadcastLeave(t *testing.T) {
	// given
	hub := newHub(t, 8)
	const sessionCount = 8
	sessions := make([]*chat.Session, sessionCount)
	joinErrors := make([]error, sessionCount)
	var wait sync.WaitGroup
	wait.Add(sessionCount)
	for index := range sessionCount {
		go func() {
			defer wait.Done()
			sessions[index], joinErrors[index] = hub.Join(
				"Main",
				string(rune('a'+index)),
			)
		}()
	}
	wait.Wait()
	for _, err := range joinErrors {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range sessions {
		drainMessages(session)
	}
	wait.Add(sessionCount * 2)

	// when
	for _, session := range sessions {
		go func() {
			defer wait.Done()
			_ = hub.Broadcast(session, []byte("message"))
		}()
		go func() {
			defer wait.Done()
			hub.Leave(session)
		}()
	}
	wait.Wait()

	// then
	for _, session := range sessions {
		select {
		case <-session.Done():
		default:
			t.Fatal("session remained active after concurrent leave")
		}
		if hub.Leave(session) {
			t.Fatal("concurrent session removed twice")
		}
	}
}

func TestHub_RejectsForeignSession(t *testing.T) {
	// given
	firstHub := newHub(t, 8)
	secondHub := newHub(t, 8)
	first := join(t, firstHub, "main", "first")
	second := join(t, secondHub, "main", "second")

	// when
	err := firstHub.Broadcast(second, []byte("message"))
	removed := firstHub.Leave(second)

	// then
	if !errors.Is(err, chat.ErrSessionClosed) {
		t.Fatalf("broadcast error = %v, want %v", err, chat.ErrSessionClosed)
	}
	if removed {
		t.Fatal("foreign session was removed")
	}
	assertActive(t, first)
	assertActive(t, second)
	assertNoMessage(t, first)
	assertNoMessage(t, second)
}

func TestNewHub_RejectsNonPositiveQueueSize(t *testing.T) {
	for _, size := range []int{-1, 0} {
		if _, err := chat.NewHub(size); err == nil {
			t.Fatalf("NewHub(%d) succeeded", size)
		}
	}
}

func loadParityFixture(t *testing.T) parityFixture {
	t.Helper()
	contents, err := os.ReadFile("testdata/java-parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture parityFixture
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func applyFixtureAction(
	t *testing.T,
	hub *chat.Hub,
	sessions map[string]*chat.Session,
	specs map[string]fixtureSession,
	action fixtureAction,
) {
	t.Helper()
	switch action.Kind {
	case "open":
		sessions[action.SessionID] = joinFixtureSession(t, hub, specs[action.SessionID])
	case "text-message":
		payload, err := base64.StdEncoding.DecodeString(*action.PayloadBase64)
		if err != nil {
			t.Fatal(err)
		}
		if err := hub.Broadcast(sessions[action.SessionID], payload); err != nil {
			t.Fatal(err)
		}
	case "normal-close":
		if !hub.Leave(sessions[action.SessionID]) {
			t.Fatal("fixture session was not active")
		}
	default:
		t.Fatalf("unsupported fixture action %q", action.Kind)
	}
}

func assertFixtureDeliveries(
	t *testing.T,
	sessions map[string]*chat.Session,
	deliveries []fixtureDelivery,
) {
	t.Helper()
	for _, delivery := range deliveries {
		if delivery.MessageType != "text" {
			t.Fatalf("fixture message type = %q, want text", delivery.MessageType)
		}
		payload, err := base64.StdEncoding.DecodeString(delivery.PayloadBase64)
		if err != nil {
			t.Fatal(err)
		}
		assertMessage(t, sessions[delivery.RecipientSessionID], payload)
	}
}

func assertNoDuplicateDeliveries(
	t *testing.T,
	sessions map[string]*chat.Session,
	deliveries []fixtureDelivery,
) {
	t.Helper()
	for _, delivery := range deliveries {
		assertNoMessage(t, sessions[delivery.RecipientSessionID])
	}
}

func assertSilentIDs(t *testing.T, sessions map[string]*chat.Session, ids []string) {
	t.Helper()
	for _, id := range ids {
		assertNoMessage(t, sessions[id])
	}
}

func assertSilent(t *testing.T, sessions map[string]*chat.Session) {
	t.Helper()
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	assertSilentIDs(t, sessions, ids)
}

func joinFixtureSession(t *testing.T, hub *chat.Hub, spec fixtureSession) *chat.Session {
	t.Helper()
	return join(t, hub, spec.Topic, spec.Username)
}

func join(t *testing.T, hub *chat.Hub, topic, username string) *chat.Session {
	t.Helper()
	session, err := hub.Join(topic, username)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func newHub(t *testing.T, queueSize int) *chat.Hub {
	t.Helper()
	hub, err := chat.NewHub(queueSize)
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func assertMessage(t *testing.T, session *chat.Session, want []byte) {
	t.Helper()
	select {
	case got := <-session.Messages():
		if string(got) != string(want) {
			t.Fatalf("message = %q, want %q", got, want)
		}
	default:
		t.Fatalf("no message received, want %q", want)
	}
}

func assertNoMessage(t *testing.T, session *chat.Session) {
	t.Helper()
	select {
	case got := <-session.Messages():
		t.Fatalf("unexpected message %q", got)
	default:
	}
}

func assertDone(t *testing.T, session *chat.Session, want chat.RemovalReason) {
	t.Helper()
	select {
	case <-session.Done():
	default:
		t.Fatal("session remained active")
	}
	if got := session.RemovalReason(); got != want {
		t.Fatalf("removal reason = %d, want %d", got, want)
	}
}

func assertActive(t *testing.T, session *chat.Session) {
	t.Helper()
	select {
	case <-session.Done():
		t.Fatalf("session removed with reason %d", session.RemovalReason())
	default:
	}
}

func drainMessages(session *chat.Session) {
	for {
		select {
		case <-session.Messages():
		default:
			return
		}
	}
}
