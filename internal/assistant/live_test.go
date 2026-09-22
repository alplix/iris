package assistant

import (
	"context"
	"testing"

	"github.com/alplix/iris/internal/tilvar"
)

// TestLiveModelFollowsTheActionFormat checks, against the REAL Tilvar server,
// that the model actually emits the strict "ACTION: {json}" line when asked
// to do something and plain text otherwise. This is the one property that
// cannot be verified with a fake server: it depends on how the real model
// behaves. Skipped unless IRIS_TILVAR_API_KEY is set (never in CI).
func TestLiveModelFollowsTheActionFormat(t *testing.T) {
	if !tilvar.Available() {
		t.Skip("set IRIS_TILVAR_API_KEY to run this against the real Tilvar server")
	}
	mgr := newTestManager(t)
	s := NewSession(mgr)

	cases := []struct {
		name    string
		ask     string
		wantAct bool
	}{
		{"plain question", "Bugün kaç görev tamamladım?", false},
		{"unrelated chit-chat", "Merhaba, nasılsın?", false},
		{"clear pause request", "Demo Server'daki Einstein@Home projesini duraklat", true},
		{"clear resume all", "Bütün sunuculardaki çalışmayı devam ettir", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := s.Ask(context.Background(), c.ask)
			if err != nil {
				t.Fatal(err)
			}
			gotAct := r.Action != nil
			t.Logf("ask=%q -> action=%v clarify=%+v text=%q", c.ask, r.Action, r.Clarify, r.Text)
			if gotAct != c.wantAct {
				t.Errorf("ask=%q: got action=%v, want %v", c.ask, gotAct, c.wantAct)
			}
		})
	}
}
