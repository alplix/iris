package tilvar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func withKey(t *testing.T, key string) {
	t.Helper()
	old := os.Getenv("IRIS_TILVAR_API_KEY")
	os.Setenv("IRIS_TILVAR_API_KEY", key)
	t.Cleanup(func() { os.Setenv("IRIS_TILVAR_API_KEY", old) })
}

func TestAvailableFollowsTheEnvOverride(t *testing.T) {
	withKey(t, "")
	if Available() {
		t.Fatal("no key configured, must report unavailable")
	}
	withKey(t, "test-key")
	if !Available() {
		t.Fatal("env override should make it available")
	}
}

func serverEcho(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	withKey(t, "test-key")
	return &Client{HTTPClient: srv.Client(), Endpoint: srv.URL, MaxRetryWait: 30 * time.Millisecond}
}

func TestChatSendsAuthAndParsesReply(t *testing.T) {
	var gotAuth string
	var gotReq request
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotReq)
		json.NewEncoder(w).Encode(response{Reply: "merhaba", Kind: "chat"})
	})
	reply, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "selam"}})
	if err != nil {
		t.Fatal(err)
	}
	if reply != "merhaba" {
		t.Errorf("reply = %q", reply)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotReq.Web || gotReq.Think {
		t.Errorf("web/think must stay false: %+v", gotReq)
	}
	if len(gotReq.Messages) != 1 || gotReq.Messages[0].Content != "selam" {
		t.Errorf("messages = %+v", gotReq.Messages)
	}
}

func TestChatWithoutAKeyFailsLocallyWithoutARequest(t *testing.T) {
	withKey(t, "")
	called := false
	c := &Client{HTTPClient: http.DefaultClient, Endpoint: "http://127.0.0.1:0"}
	_, _, err := c.attempt(context.Background(), nil)
	if called {
		t.Fatal("must not make a network call without a key")
	}
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestChatUnauthorized(t *testing.T) {
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"error": "login required"})
	})
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}})
	var tErr *Error
	if err == nil || !asTilvarError(err, &tErr) || !tErr.Unauthorized() {
		t.Fatalf("expected an Unauthorized *Error, got %v", err)
	}
	if tErr.Message != "login required" {
		t.Errorf("message = %q, should surface the server's own {\"error\":...} field", tErr.Message)
	}
}

func TestChatDetailFieldIsAlsoUnderstood(t *testing.T) {
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"detail": "message must not be empty"})
	})
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}})
	if err == nil || err.Error() != "message must not be empty" {
		t.Fatalf("err = %v", err)
	}
}

func TestChatRetriesOn429ThenSucceeds(t *testing.T) {
	n := 0
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "slow down"})
			return
		}
		json.NewEncoder(w).Encode(response{Reply: "ok"})
	})
	reply, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}})
	if err != nil || reply != "ok" {
		t.Fatalf("reply=%q err=%v", reply, err)
	}
	if n != 2 {
		t.Errorf("expected exactly one retry, server was hit %d times", n)
	}
}

func TestChatGivesUpAfterRepeated429(t *testing.T) {
	n := 0
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]string{"error": "slow down"})
	})
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("expected an error once retries are exhausted")
	}
	if n < 2 || n > 5 {
		t.Errorf("expected a small bounded number of attempts, got %d", n)
	}
}

func TestChatDoesNotRetryOn400(t *testing.T) {
	n := 0
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "bad request"})
	})
	_, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if n != 1 {
		t.Errorf("a 400 is the caller's fault and must not be retried, got %d attempts", n)
	}
}

func TestChatRetryAfterIsBoundedNotHonoredBlindly(t *testing.T) {
	n := 0
	start := time.Now()
	c := serverEcho(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "9999")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "busy"})
			return
		}
		json.NewEncoder(w).Encode(response{Reply: "ok"})
	})
	if _, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: "x"}}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("a huge Retry-After must be clamped to MaxRetryWait, waited %v", time.Since(start))
	}
}

func TestTrimKeepsTheLatestMessageWhole(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: string(make([]byte, 900))},
		{Role: RoleAssistant, Content: string(make([]byte, 900))},
		{Role: RoleUser, Content: "the real question"},
	}
	out := Trim(msgs, 500)
	if out[len(out)-1].Content != "the real question" {
		t.Fatalf("the latest message must survive trimming: %+v", out)
	}
	total := 0
	for _, m := range out {
		total += len(m.Content)
	}
	if len(out) > 1 && total > 500 {
		t.Errorf("still over budget after trimming: %d chars in %d messages", total, len(out))
	}
}

// TestChatAgainstRealServerManual hits the real Tilvar endpoint. It is
// skipped unless IRIS_TILVAR_API_KEY is set (never in CI), so it stays out of
// the shared quota except when a developer runs it on purpose.
func TestChatAgainstRealServerManual(t *testing.T) {
	if !Available() {
		t.Skip("set IRIS_TILVAR_API_KEY to run this against the real Tilvar server")
	}
	c := NewClient()
	reply, err := c.Chat(context.Background(), []Message{{Role: RoleUser, Content: `Bu bir test. Sadece "ok" yaz.`}})
	if err != nil {
		t.Fatal(err)
	}
	if reply == "" {
		t.Fatal("empty reply")
	}
	t.Logf("real reply: %s", reply)
}

func TestTrimIsANoopWhenAlreadyUnderBudget(t *testing.T) {
	msgs := []Message{{Role: RoleUser, Content: "hi"}, {Role: RoleAssistant, Content: "hello"}}
	out := Trim(msgs, 1800)
	if len(out) != 2 {
		t.Errorf("should not have trimmed anything, got %d messages", len(out))
	}
}
