package boinc

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestBoincPasswdHash(t *testing.T) {
	// BOINC's convention: md5(password + lowercase(email)); mixed-case email
	// must hash the same as the lowercased form.
	sum := md5.Sum([]byte("secret" + "user@example.com"))
	want := fmt.Sprintf("%x", sum)
	if got := boincPasswdHash("secret", "USER@Example.com"); got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}

func TestLookupAccountSendsPasswdHashNotPlaintext(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		fmt.Fprint(w, `<account_out><authenticator>abc123</authenticator></account_out>`)
	}))
	defer srv.Close()

	auth, err := LookupAccount(srv.URL, "User@Example.com", "hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "abc123" {
		t.Errorf("authenticator = %q", auth)
	}
	if gotQuery.Get("passwd") != "" {
		t.Error("the plaintext password must never be sent")
	}
	want := boincPasswdHash("hunter2", "User@Example.com")
	if got := gotQuery.Get("passwd_hash"); got != want {
		t.Errorf("passwd_hash = %s, want %s", got, want)
	}
	if gotQuery.Get("email_addr") != "User@Example.com" {
		t.Errorf("email_addr = %s", gotQuery.Get("email_addr"))
	}
}

func TestLookupAccountReportsTheServersErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<account_out><error_num>-136</error_num><error_msg>Email address not found</error_msg></account_out>`)
	}))
	defer srv.Close()

	_, err := LookupAccount(srv.URL, "nobody@example.com", "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != "Email address not found ("+srv.URL+")" {
		t.Errorf("error = %q", got)
	}
}

func TestLookupAccountFallsBackToErrorNum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<account_out><error_num>-137</error_num></account_out>`)
	}))
	defer srv.Close()

	_, err := LookupAccount(srv.URL, "nobody@example.com", "x")
	if err == nil {
		t.Fatal("expected an error")
	}
}
