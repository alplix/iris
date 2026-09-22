// Package assistant wires Iris' manager to the Tilvar chat API: it builds a
// safe, bounded summary of the fleet for context, recognises the model's
// requests to change something and gates them behind explicit confirmation,
// and keeps the (in-memory only) conversation history the stateless API
// itself does not.
package assistant

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/tilvar"
)

// MaxMessageChars is a hard ceiling on one person's chat turn, applied before
// the per-request budget below even comes into it (see wrapAndBudget).
const MaxMessageChars = 1500

// requestBudget leaves headroom under the server's documented per-request
// character cap (see tilvar.MaxRequestChars).
const requestBudget = tilvar.MaxRequestChars - 100

// maxHistoryMessages caps how much (visible, raw) history a session keeps;
// tilvar.Trim bounds what actually gets sent to fit the request budget.
const maxHistoryMessages = 40

// Session is one ongoing conversation with the assistant for one running
// Iris manager. It keeps history in memory only — nothing is written to
// disk, matching the server's own "no history kept" behaviour.
type Session struct {
	mgr    *app.Manager
	client *tilvar.Client

	mu      sync.Mutex
	history []tilvar.Message // exactly what is shown to the person, in order
	pending map[string]*plan
}

func NewSession(mgr *app.Manager) *Session {
	return &Session{mgr: mgr, client: tilvar.NewClient(), pending: map[string]*plan{}}
}

// ActionCard is a proposed fleet change awaiting the person's confirmation.
// It carries only structured facts (never a ready-made sentence) so the
// frontend can render it in the person's own language.
type ActionCard struct {
	ID      string            `json:"id"`
	Tool    string            `json:"tool"`
	Host    string            `json:"host,omitempty"`
	Project string            `json:"project,omitempty"`
	Op      string            `json:"op,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Reply is exactly one of: a chat answer to show (Text), a change awaiting
// confirmation (Action), or an explanation of why a requested change could
// not be resolved (Clarify) — never more than one at a time.
type Reply struct {
	Text    string      `json:"text,omitempty"`
	Action  *ActionCard `json:"action,omitempty"`
	Clarify *Clarify    `json:"clarify,omitempty"`
}

// History returns the visible conversation so far.
func (s *Session) History() []tilvar.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]tilvar.Message(nil), s.history...)
}

// Reset clears the conversation and discards any pending confirmation.
func (s *Session) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = nil
	s.pending = map[string]*plan{}
}

// Ask sends the person's message to the assistant and returns either a reply
// to show or a control action awaiting their explicit confirmation. Nothing
// on the fleet ever changes from this call alone.
func (s *Session) Ask(ctx context.Context, text string) (Reply, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Reply{}, fmt.Errorf("empty message")
	}
	if !tilvar.Available() {
		return Reply{}, fmt.Errorf("the assistant is not available in this build")
	}
	if r := []rune(text); len(r) > MaxMessageChars {
		text = string(r[:MaxMessageChars])
	}

	s.mu.Lock()
	history := append([]tilvar.Message(nil), s.history...)
	s.mu.Unlock()

	// The current turn must fit the request budget entirely on its own —
	// with zero history — since Trim below can only ever drop older history,
	// never the live message. The fleet digest varies with the size of the
	// fleet, so it is the user's own text that gets shortened if needed,
	// never the instructions or the digest itself (both are load-bearing).
	digest := buildDigest(s.mgr)
	overhead := len(actionInstructions) + len(digest) + len(wrapUserMessage("", ""))
	userBudget := requestBudget - overhead
	if userBudget < 100 {
		userBudget = 100
	}
	if r := []rune(text); len(r) > userBudget {
		text = string(r[:userBudget])
	}
	wrapped := wrapUserMessage(digest, text)
	toSend := tilvar.Trim(append(history, tilvar.Message{Role: tilvar.RoleUser, Content: wrapped}), requestBudget)

	reply, err := s.client.Chat(ctx, toSend)
	if err != nil {
		return Reply{}, err
	}

	// The raw "ACTION: {...}" line is never worth keeping in history: not for
	// display (the person sees a proper confirmation card instead, built by
	// the frontend from the structured Reply below, never from history) and
	// not really for the model either, so store a short plain-English note
	// in its place — it costs fewer characters on every future turn than the
	// JSON would.
	displayReply := reply
	a, isAction := parseAction(reply)
	if isAction {
		displayReply = actionHistoryNote(a)
	}

	s.mu.Lock()
	s.history = append(s.history, tilvar.Message{Role: tilvar.RoleUser, Content: text}, tilvar.Message{Role: tilvar.RoleAssistant, Content: displayReply})
	if len(s.history) > maxHistoryMessages {
		s.history = s.history[len(s.history)-maxHistoryMessages:]
	}
	s.mu.Unlock()

	if !isAction {
		return Reply{Text: reply}, nil
	}
	p, clarify := resolve(s.mgr, a)
	if clarify != nil {
		// The model tried to act on something that does not match the fleet
		// it was just shown; fail safe and explain rather than guessing.
		return Reply{Clarify: clarify}, nil
	}
	id := newActionID()
	s.mu.Lock()
	s.pending[id] = p
	s.mu.Unlock()
	return Reply{Action: &ActionCard{ID: id, Tool: a.Tool, Host: a.Host, Project: a.Project, Op: a.Op, Mode: a.Mode, Fields: a.Fields}}, nil
}

// actionHistoryNote is what stands in for a proposed action in the
// conversation history sent to the model on later turns (see above).
func actionHistoryNote(a action) string {
	switch a.Tool {
	case "project_op":
		return fmt.Sprintf("(Proposed: %s project %q on %q)", a.Op, a.Project, a.Host)
	case "client_op":
		return fmt.Sprintf("(Proposed: %s %s on %q)", a.Op, a.Mode, a.Host)
	case "client_op_all":
		return fmt.Sprintf("(Proposed: %s %s on all servers)", a.Op, a.Mode)
	case "set_prefs":
		return fmt.Sprintf("(Proposed: update preferences on %q)", a.Host)
	default:
		return "(Proposed an action)"
	}
}

// Confirm runs a pending action the person approved. The result is not sent
// back to the model — it is reported to the person directly, saving a
// round trip against the shared quota for something Iris can say itself.
func (s *Session) Confirm(id string) error {
	s.mu.Lock()
	p, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("that action is no longer pending")
	}
	return p.Run()
}

// Cancel discards a pending action without running it.
func (s *Session) Cancel(id string) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

func newActionID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// actionInstructions is sent with every turn (the API keeps no memory of its
// own), so it stays deliberately short: it is overhead on every single
// request against a shared, rate-limited quota.
const actionInstructions = `You can control this Iris (BOINC-compatible) fleet. If, and only if, the user clearly asks to change something, reply with EXACTLY one line and nothing else — no explanation before or after it:
ACTION: {"tool":"project_op","host":"<server>","project":"<project>","op":"suspend|resume|update|detach|nomorework|allowmorework"}
ACTION: {"tool":"client_op","host":"<server>","op":"setRunMode|setNetworkMode|benchmarks","mode":"always|auto|never"}
ACTION: {"tool":"client_op_all","op":"...","mode":"..."} (every server)
ACTION: {"tool":"set_prefs","host":"<server>","fields":{"max_ncpus_pct":"50"}}
Use the exact server/project names from the summary below; never invent one. Otherwise just answer normally in plain text.`

func wrapUserMessage(digest, text string) string {
	return actionInstructions + "\n\n" + digest + "\nUser: " + text
}
