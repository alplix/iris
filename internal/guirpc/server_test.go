package guirpc

import (
	"bytes"
	"net"
	"testing"
	"time"
)

type stubHandler struct{ Handler }

func startTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(stubHandler{}, "pw")
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Stop)
	return s
}

func TestUnauthenticatedPeerCannotMakeTheServerBufferWithoutLimit(t *testing.T) {
	s := startTestServer(t)
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// An opening tag that never closes keeps the request "incomplete" forever;
	// before the cap this grew the server's buffer until the connection timed out.
	chunk := bytes.Repeat([]byte("<a>\n"), 16*1024)
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	for i := 0; i < 64; i++ { // 4 MiB, far past the unauthenticated limit
		if _, err := conn.Write(chunk); err != nil {
			break // the server hung up: that is the point
		}
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 16)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected the server to drop the connection")
	} else if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("the server kept the oversized request open instead of closing it")
	}
}

func TestAuthenticationStillWorksUnderTheCap(t *testing.T) {
	s := startTestServer(t)
	conn, err := net.Dial("tcp", s.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte("<auth1/>\n\x03"))
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil || !bytes.Contains(buf[:n], []byte("<nonce>")) {
		t.Fatalf("no nonce: %v %q", err, buf[:n])
	}
}
