// Package tilvar is a thin client for the Tilvar chat API that powers Iris'
// optional AI assistant. The endpoint is stateless (it keeps no conversation
// history) and shared across every Iris installation behind one quota, so
// this client is deliberately careful about how much it sends and how often
// it retries.
package tilvar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Endpoint is the Tilvar chat API Iris talks to.
const Endpoint = "https://tilvar.athena.org.tr/api/chat"

// APIKey is baked into official release builds via
// -ldflags "-X github.com/alplix/iris/internal/tilvar.APIKey=...". A local or
// unofficial build leaves it empty, which disables the assistant unless the
// person building it sets IRIS_TILVAR_API_KEY themselves.
var APIKey string

// Key returns the key to use: an environment override (for local development)
// or the one built into the binary.
func Key() string {
	if v := os.Getenv("IRIS_TILVAR_API_KEY"); v != "" {
		return v
	}
	return APIKey
}

// Available reports whether this build can reach the assistant at all.
func Available() bool { return Key() != "" }

// Role is either "user" or "assistant" — the only two the API accepts.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// MaxRequestChars is the server's documented per-request character budget.
// Client keeps some headroom under it (see Trim).
const MaxRequestChars = 2000

type request struct {
	Messages []Message `json:"messages"`
	Web      bool      `json:"web"`
	Think    bool      `json:"think"`
}

type response struct {
	Reply string `json:"reply"`
	Kind  string `json:"kind"`
}

// errPayload covers both shapes the server has been observed to send:
// {"detail": "..."} per its own docs, and {"error": "..."} in practice.
type errPayload struct {
	Detail string `json:"detail"`
	Error  string `json:"error"`
}

// Error is returned for a non-200 response. Unauthorized means the request
// used a missing/invalid key (never the user's fault); RetryAfter is set for
// a rate limit or a busy server (Iris should back off and try again later,
// never surface a raw error to the person for those).
type Error struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unauthorized() bool { return e.StatusCode == http.StatusUnauthorized }
func (e *Error) Throttled() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode == http.StatusServiceUnavailable
}

// Client calls the Tilvar chat API.
type Client struct {
	HTTPClient *http.Client
	Endpoint   string

	// MaxAttempts and MaxRetryWait govern the 429/503 backoff below; both
	// default (0) to sensible production values and exist mainly so tests
	// don't have to sleep through a real backoff.
	MaxAttempts  int
	MaxRetryWait time.Duration
}

func NewClient() *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		Endpoint:   Endpoint,
	}
}

// Chat sends messages and returns the assistant's reply text. On 429/503 it
// retries after the server's Retry-After delay (bounded), exactly as the API
// asks callers to; the caller never sees those as errors unless every retry
// is exhausted.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	maxWait := c.MaxRetryWait
	if maxWait <= 0 {
		maxWait = 20 * time.Second
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		reply, wait, err := c.attempt(ctx, messages)
		if err == nil {
			return reply, nil
		}
		var tErr *Error
		if !asTilvarError(err, &tErr) || !tErr.Throttled() || attempt == maxAttempts-1 {
			return "", err
		}
		lastErr = err
		if wait > maxWait {
			wait = maxWait
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "", lastErr
}

func asTilvarError(err error, out **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*out = e
	}
	return ok
}

func (c *Client) attempt(ctx context.Context, messages []Message) (reply string, retryAfter time.Duration, err error) {
	if !Available() {
		return "", 0, &Error{Message: "the assistant is not configured in this build"}
	}
	body, err := json.Marshal(request{Messages: messages, Web: false, Think: false})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+Key())

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", 0, &Error{Message: fmt.Sprintf("could not reach the assistant: %v", err)}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode != http.StatusOK {
		wait := parseRetryAfter(resp.Header.Get("Retry-After"))
		return "", wait, &Error{StatusCode: resp.StatusCode, Message: errMessage(resp.StatusCode, data), RetryAfter: wait}
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil || r.Reply == "" {
		return "", 0, &Error{Message: "the assistant sent back something unreadable"}
	}
	return r.Reply, 0, nil
}

func errMessage(status int, body []byte) string {
	var p errPayload
	json.Unmarshal(body, &p)
	if p.Detail != "" {
		return p.Detail
	}
	if p.Error != "" {
		return p.Error
	}
	switch status {
	case http.StatusUnauthorized:
		return "the assistant's key was rejected"
	case http.StatusTooManyRequests:
		return "the assistant is busy right now"
	case http.StatusServiceUnavailable:
		return "the assistant is temporarily unavailable"
	case http.StatusRequestEntityTooLarge:
		return "that message is too long"
	default:
		return fmt.Sprintf("the assistant returned HTTP %d", status)
	}
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 3 * time.Second
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 3 * time.Second
}

// Trim keeps the total content of messages under budget (leaving headroom
// under the server's MaxRequestChars) by dropping the oldest history first.
// The last message (the live user turn) is always kept whole, since it is
// the one thing that must never silently disappear.
func Trim(messages []Message, budget int) []Message {
	if budget <= 0 {
		budget = MaxRequestChars - 200
	}
	total := 0
	for _, m := range messages {
		total += len(m.Content)
	}
	if total <= budget || len(messages) <= 1 {
		return messages
	}
	// Drop oldest messages (but never the last one) until it fits.
	out := append([]Message(nil), messages...)
	for len(out) > 1 && total > budget {
		total -= len(out[0].Content)
		out = out[1:]
	}
	return out
}
