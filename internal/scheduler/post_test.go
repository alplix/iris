package scheduler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A project's http:// address that redirects (301) to https:// must still
// receive the POST body; Go's default client would resend it as an empty GET.
func TestPostFollowKeepsTheBodyAcrossRedirects(t *testing.T) {
	var got string
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = r.Method + ":" + string(b)
		io.WriteString(w, "ok")
	}))
	defer final.Close()
	for _, code := range []int{301, 302, 303, 307, 308} {
		front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, final.URL+"/handler/", code)
		}))
		calls := 0
		resp, err := postFollow(context.Background(), http.DefaultClient, front.URL+"/handler", "text/xml", func() (io.Reader, int64, error) {
			calls++
			return strings.NewReader("<request/>"), 10, nil
		})
		front.Close()
		if err != nil {
			t.Fatalf("%d: %v", code, err)
		}
		resp.Body.Close()
		if got != "POST:<request/>" || calls != 2 {
			t.Errorf("%d: server received %q after %d attempt(s), want the POST body on the second", code, got, calls)
		}
	}
}

func TestPostFollowGivesUpOnARedirectLoop(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/again", http.StatusMovedPermanently)
	}))
	defer srv.Close()
	_, err := postFollow(context.Background(), http.DefaultClient, srv.URL, "text/xml", func() (io.Reader, int64, error) {
		return strings.NewReader("x"), 1, nil
	})
	if err == nil {
		t.Error("a redirect loop must end in an error")
	}
}

// The real failure: the upload address redirects, and the file must still be
// uploaded (with the certificate) at the new place.
func TestUploadResultFileFollowsARedirect(t *testing.T) {
	var got string
	back := uploadServer(t, 0, "0", &got)
	defer back.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, back.URL, http.StatusMovedPermanently)
	}))
	defer front.Close()
	err := UploadResultFile(context.Background(), ResultUpload{Name: "f", Path: writeTemp(t, "0123456789"), URLs: []string{front.URL}, MaxNBytes: 100, Signature: "SIG"}, "8.0.2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "0123456789|") {
		t.Errorf("the file must arrive at the redirect target, got %q", got)
	}
}
