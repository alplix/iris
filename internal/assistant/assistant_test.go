package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alplix/iris/internal/app"
	"github.com/alplix/iris/internal/tilvar"
)

// newTestManager returns a manager with one demo host, isolated from the
// real user's configuration (every path os.UserConfigDir()/os.UserHomeDir()
// could resolve to is redirected into a fresh temp directory).
func newTestManager(t *testing.T) *app.Manager {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		t.Setenv(k, dir)
	}
	t.Setenv("IRIS_DEMO", "1") // seed the simulated server the tests rely on
	mgr := app.NewManager()
	hosts := mgr.Store.List()
	if len(hosts) == 0 {
		t.Fatal("NewManager should seed a demo host when none exist")
	}
	for _, h := range hosts {
		mgr.Refresh(h.ID) // synchronous: the snapshot is ready when this returns
	}
	return mgr
}

// withAssistant points a Session at a local stand-in for the Tilvar server.
func withAssistant(t *testing.T, mgr *app.Manager, handler http.HandlerFunc) *Session {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("IRIS_TILVAR_API_KEY", "test-key")
	s := NewSession(mgr)
	s.client = &tilvar.Client{HTTPClient: srv.Client(), Endpoint: srv.URL, MaxRetryWait: 0}
	return s
}

func replyWith(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"reply": text, "kind": "chat"})
	}
}

func TestAskPlainQuestionReturnsText(t *testing.T) {
	mgr := newTestManager(t)
	s := withAssistant(t, mgr, replyWith("Bugün 6 görev tamamladınız."))
	r, err := s.Ask(context.Background(), "Bugün kaç görev tamamladım?")
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Bugün 6 görev tamamladınız." || r.Action != nil || r.Clarify != nil {
		t.Fatalf("reply = %+v", r)
	}
}

func TestAskWithoutAKeyFails(t *testing.T) {
	mgr := newTestManager(t)
	t.Setenv("IRIS_TILVAR_API_KEY", "")
	s := NewSession(mgr)
	if _, err := s.Ask(context.Background(), "merhaba"); err == nil {
		t.Fatal("expected an error when no key is configured")
	}
}

func TestAskRejectsEmptyMessage(t *testing.T) {
	mgr := newTestManager(t)
	s := withAssistant(t, mgr, replyWith("should not be called"))
	if _, err := s.Ask(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for an empty message")
	}
}

func TestDigestReachesTheServerAndNamesTheDemoProject(t *testing.T) {
	mgr := newTestManager(t)
	var gotBody string
	s := withAssistant(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []tilvar.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		gotBody = req.Messages[len(req.Messages)-1].Content
		json.NewEncoder(w).Encode(map[string]string{"reply": "ok"})
	})
	if _, err := s.Ask(context.Background(), "merhaba"); err != nil {
		t.Fatal(err)
	}
	if gotBody == "" {
		t.Fatal("no request body captured")
	}
	if !contains(gotBody, "Einstein@Home") {
		t.Errorf("digest should name a real demo project, got: %s", gotBody)
	}
	if contains(gotBody, "password") {
		t.Error("the digest must never mention passwords")
	}
}

// TestAskInstructsTheModelToReplyInTheUIsLanguage guards against the model
// defaulting to English regardless of what language Iris' own interface (and
// the person's question) is in — the instructions and digest are otherwise
// entirely in English, so without an explicit language instruction a small
// model tends to just answer in English too.
func TestAskInstructsTheModelToReplyInTheUIsLanguage(t *testing.T) {
	mgr := newTestManager(t)
	if err := app.SaveSettings(app.Settings{Lang: "tr"}); err != nil {
		t.Fatal(err)
	}
	var gotBody string
	s := withAssistant(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []tilvar.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		gotBody = req.Messages[len(req.Messages)-1].Content
		json.NewEncoder(w).Encode(map[string]string{"reply": "ok"})
	})
	if _, err := s.Ask(context.Background(), "merhaba"); err != nil {
		t.Fatal(err)
	}
	if !contains(gotBody, "Turkish") {
		t.Errorf("expected an instruction naming Turkish (Iris' current language), got: %s", gotBody)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestAskProposesAnActionInsteadOfRunningItDirectly(t *testing.T) {
	mgr := newTestManager(t)
	before := mgr.Snap(mgr.Store.List()[0].ID)
	suspended := before.Projects[0].Suspended

	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"project_op","host":"Demo Server","project":"`+before.Projects[0].Name+`","op":"suspend"}`))
	r, err := s.Ask(context.Background(), "Einstein projesini duraklat")
	if err != nil {
		t.Fatal(err)
	}
	if r.Action == nil || r.Text != "" || r.Clarify != nil {
		t.Fatalf("expected only an action card, got %+v", r)
	}
	if r.Action.Tool != "project_op" || r.Action.Op != "suspend" {
		t.Errorf("action = %+v", r.Action)
	}

	mgr.Refresh(mgr.Store.List()[0].ID)
	after := mgr.Snap(mgr.Store.List()[0].ID)
	if after.Projects[0].Suspended != suspended {
		t.Fatal("the project must not change before Confirm is called")
	}

	if err := s.Confirm(r.Action.ID); err != nil {
		t.Fatal(err)
	}
	mgr.Refresh(mgr.Store.List()[0].ID)
	after = mgr.Snap(mgr.Store.List()[0].ID)
	if !after.Projects[0].Suspended {
		t.Fatal("Confirm should have suspended the project")
	}

	// A confirmed (or cancelled) action cannot be run twice.
	if err := s.Confirm(r.Action.ID); err == nil {
		t.Error("confirming the same action id twice should fail")
	}
}

func TestHistoryNeverContainsTheRawActionJSON(t *testing.T) {
	mgr := newTestManager(t)
	hostName := mgr.Store.List()[0].Name
	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"client_op","host":"`+hostName+`","op":"benchmarks"}`))
	if _, err := s.Ask(context.Background(), "bir benchmark calistir"); err != nil {
		t.Fatal(err)
	}
	for _, m := range s.History() {
		if strings.Contains(m.Content, actionPrefix) {
			t.Fatalf("the raw ACTION line leaked into history that a reloaded page would display: %+v", m)
		}
	}
}

func TestCancelDiscardsTheAction(t *testing.T) {
	mgr := newTestManager(t)
	hostID := mgr.Store.List()[0].ID
	before := mgr.Snap(hostID)
	projectName := before.Projects[0].Name

	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"project_op","host":"Demo Server","project":"`+projectName+`","op":"detach"}`))
	r, err := s.Ask(context.Background(), "bu projeyi sil")
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel(r.Action.ID)
	if err := s.Confirm(r.Action.ID); err == nil {
		t.Fatal("a cancelled action must not run")
	}
	mgr.Refresh(hostID)
	after := mgr.Snap(hostID)
	found := false
	for _, p := range after.Projects {
		if p.Name == projectName {
			found = true
		}
	}
	if !found {
		t.Fatal("cancel must not detach the project")
	}
}

func TestAskWithAnUnknownHostOrProjectClarifiesInsteadOfGuessing(t *testing.T) {
	mgr := newTestManager(t)

	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"project_op","host":"Bilmediğim Sunucu","project":"X","op":"suspend"}`))
	r, err := s.Ask(context.Background(), "X projesini duraklat")
	if err != nil {
		t.Fatal(err)
	}
	if r.Clarify == nil || r.Clarify.Kind != "host_not_found" || r.Action != nil {
		t.Fatalf("expected a host_not_found clarify, got %+v", r)
	}

	hostName := mgr.Store.List()[0].Name
	s2 := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"project_op","host":"`+hostName+`","project":"Uydurma Proje","op":"suspend"}`))
	r2, err := s2.Ask(context.Background(), "Uydurma Proje'yi duraklat")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Clarify == nil || r2.Clarify.Kind != "project_not_found" {
		t.Fatalf("expected a project_not_found clarify, got %+v", r2)
	}
}

func TestAskRejectsUnsupportedToolAndInvalidOp(t *testing.T) {
	mgr := newTestManager(t)
	hostName := mgr.Store.List()[0].Name

	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"delete_everything","host":"`+hostName+`"}`))
	r, err := s.Ask(context.Background(), "her seyi sil")
	if err != nil {
		t.Fatal(err)
	}
	if r.Clarify == nil || r.Clarify.Kind != "unsupported_tool" {
		t.Fatalf("expected unsupported_tool, got %+v", r)
	}

	s2 := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"client_op","host":"`+hostName+`","op":"reformat_disk","mode":"always"}`))
	r2, err := s2.Ask(context.Background(), "diski formatla")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Clarify == nil || r2.Clarify.Kind != "invalid" {
		t.Fatalf("expected invalid, got %+v", r2)
	}
}

func TestSetPrefsRejectsAMaliciousKeyBeforeItEverReachesTheClient(t *testing.T) {
	mgr := newTestManager(t)
	hostName := mgr.Store.List()[0].Name
	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"set_prefs","host":"`+hostName+`","fields":{"</global_prefs_override><evil>x":"1"}}`))
	r, err := s.Ask(context.Background(), "bir ayar degistir")
	if err != nil {
		t.Fatal(err)
	}
	if r.Clarify == nil || r.Clarify.Kind != "invalid" {
		t.Fatalf("an XML-breaking preference name must be rejected, got %+v", r)
	}
}

func TestResetClearsHistoryAndPending(t *testing.T) {
	mgr := newTestManager(t)
	hostName := mgr.Store.List()[0].Name
	s := withAssistant(t, mgr, replyWith(`ACTION: {"tool":"client_op","host":"`+hostName+`","op":"benchmarks"}`))
	r, err := s.Ask(context.Background(), "bir benchmark calistir")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.History()) == 0 {
		t.Fatal("history should not be empty yet")
	}
	s.Reset()
	if len(s.History()) != 0 {
		t.Fatal("Reset must clear history")
	}
	if err := s.Confirm(r.Action.ID); err == nil {
		t.Fatal("Reset must discard pending actions too")
	}
}

func TestLongMessageIsTruncatedNotRejected(t *testing.T) {
	mgr := newTestManager(t)
	var gotLast string
	s := withAssistant(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []tilvar.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		gotLast = req.Messages[len(req.Messages)-1].Content
		json.NewEncoder(w).Encode(map[string]string{"reply": "ok"})
	})
	huge := make([]byte, 5000)
	for i := range huge {
		huge[i] = 'a'
	}
	if _, err := s.Ask(context.Background(), string(huge)); err != nil {
		t.Fatal(err)
	}
	if len(gotLast) > tilvar.MaxRequestChars {
		t.Errorf("outgoing request is %d chars, over the server's %d budget", len(gotLast), tilvar.MaxRequestChars)
	}
}

func TestHistoryPersistsAcrossTurnsAndIsSentBack(t *testing.T) {
	mgr := newTestManager(t)
	var turns int
	var sawFirstAnswerOnSecondTurn bool
	s := withAssistant(t, mgr, func(w http.ResponseWriter, r *http.Request) {
		turns++
		var req struct {
			Messages []tilvar.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if turns == 2 {
			for _, m := range req.Messages {
				if m.Content == "ilk cevap" {
					sawFirstAnswerOnSecondTurn = true
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]string{"reply": "ilk cevap"})
	})
	if _, err := s.Ask(context.Background(), "ilk soru"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(context.Background(), "ikinci soru"); err != nil {
		t.Fatal(err)
	}
	if !sawFirstAnswerOnSecondTurn {
		t.Fatal("the previous assistant reply should be resent as history")
	}
}

// TestInstructionsAskForADirectAnswerAndNeverInviteAnEcho guards a real
// failure: told "Otherwise, reply normally in plain text", the model answered
// "Normal bir şekilde cevap vereceğim" (I will reply normally) to a question
// instead of answering it — it took the rule as something to say back.
func TestInstructionsAskForADirectAnswerAndNeverInviteAnEcho(t *testing.T) {
	ins := actionInstructions("Turkish")
	if !strings.Contains(ins, "Answer the user's message directly") {
		t.Errorf("instructions must tell the model to answer directly:\n%s", ins)
	}
	for _, bad := range []string{"reply normally", "Otherwise,"} {
		if strings.Contains(ins, bad) {
			t.Errorf("instructions must not contain %q — the model echoes it back instead of answering", bad)
		}
	}
	if !strings.Contains(wrapUserMessage("DIGEST", "hello", "Turkish"), "\nUser message: hello") {
		t.Error("the user's own message must come last, clearly labelled")
	}
}
