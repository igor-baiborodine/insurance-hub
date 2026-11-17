package websocket

import (
	"errors"
	"testing"

	gorilla "github.com/gorilla/websocket"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
)

func TestCloseCodeForSession_MapsBoundedRemovalReasons(t *testing.T) {
	t.Run("queue saturation gets 1013", func(t *testing.T) {
		// given
		hub := newTestHub(t, 2)
		sender := joinTestSession(t, hub, "main", "sender")
		slow := joinTestSession(t, hub, "main", "slow")
		<-sender.Messages()
		for _, payload := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
			if err := hub.Broadcast(sender, payload); err != nil {
				t.Fatal(err)
			}
		}

		// when
		code := closeCodeForSession(slow)

		// then
		if slow.RemovalReason() != chat.RemovalQueueFull ||
			code != gorilla.CloseTryAgainLater {
			t.Fatalf(
				"removal/code = %d/%d, want queue-full/%d",
				slow.RemovalReason(),
				code,
				gorilla.CloseTryAgainLater,
			)
		}
	})

	t.Run("drain gets 1001", func(t *testing.T) {
		// given
		hub := newTestHub(t, 8)
		session := joinTestSession(t, hub, "main", "alice")
		hub.BeginDrain()

		// when
		code := closeCodeForSession(session)

		// then
		if code != gorilla.CloseGoingAway {
			t.Fatalf("close code = %d, want %d", code, gorilla.CloseGoingAway)
		}
	})

	t.Run("ordinary leave gets 1000", func(t *testing.T) {
		// given
		hub := newTestHub(t, 8)
		session := joinTestSession(t, hub, "main", "alice")
		if !hub.Leave(session) {
			t.Fatal("session was not active")
		}

		// when
		code := closeCodeForSession(session)

		// then
		if code != gorilla.CloseNormalClosure {
			t.Fatalf("close code = %d, want %d", code, gorilla.CloseNormalClosure)
		}
	})
}

func TestCloseCodeForReadError_MapsProtocolFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"read limit", gorilla.ErrReadLimit, gorilla.CloseMessageTooBig},
		{
			"peer close",
			&gorilla.CloseError{Code: gorilla.CloseNormalClosure},
			gorilla.CloseNormalClosure,
		},
		{"unexpected read", errors.New("read failed"), gorilla.CloseInternalServerErr},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := closeCodeForReadError(test.err); got != test.want {
				t.Fatalf("close code = %d, want %d", got, test.want)
			}
		})
	}
}

func newTestHub(t *testing.T, queueSize int) *chat.Hub {
	t.Helper()
	hub, err := chat.NewHub(queueSize)
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func joinTestSession(t *testing.T, hub *chat.Hub, topic, username string) *chat.Session {
	t.Helper()
	session, err := hub.Join(topic, username)
	if err != nil {
		t.Fatal(err)
	}
	return session
}
