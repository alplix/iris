package guirpc

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	cxml "github.com/alplix/iris/internal/xml"
)

const ETX byte = 0x03

type Handler interface {
	GetState() ([]byte, error)
	GetCcStatus() ([]byte, error)
	GetMessages(after int) ([]byte, error)
	GetTransfers() ([]byte, error)
	GetStats() ([]byte, error)
	GetDiskUsage() ([]byte, error)
	GetDailyXferHistory() ([]byte, error)
	GetPrefsOverride() ([]byte, error)
	SetPrefsOverride(pairs [][2]string) error
	SetRunMode(mode string) error
	SetNetworkMode(mode string) error
	ResultOp(name, op string) error
	ProjectOp(url, op string) error
	FileTransferOp(name, op string) error
	ProjectAttach(url, auth, name string) error
	RunBenchmarks() error
	GetHostInfo() ([]byte, error)
}

type Server struct {
	listener net.Listener
	handler  Handler
	password string
	mu       sync.Mutex
	clients  map[net.Conn]bool
	onQuit   func()
}

func NewServer(handler Handler, password string) *Server {
	return &Server{
		handler:  handler,
		password: password,
		clients:  make(map[net.Conn]bool),
	}
}

func (s *Server) SetOnQuit(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onQuit = fn
}

func (s *Server) Start(addr string) error {
	var err error
	s.listener, err = net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	for c := range s.clients {
		c.Close()
	}
	s.mu.Unlock()
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleClient(conn)
	}
}

func (s *Server) handleClient(conn net.Conn) {
	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
	}()

	conn.SetDeadline(time.Now().Add(5 * time.Minute))
	r := bufio.NewReaderSize(conn, 64*1024)

	for {
		req, err := readRequest(r)
		if err != nil {
			return
		}
		if len(req) == 0 {
			continue
		}
		reply := s.dispatch(req)
		conn.SetWriteDeadline(time.Now().Add(12 * time.Second))
		conn.Write([]byte(reply))
		conn.Write([]byte{ETX})
		conn.SetDeadline(time.Now().Add(5 * time.Minute))
	}
}

func readRequest(r *bufio.Reader) (string, error) {
	var buf bytes.Buffer
	depth := 0
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return buf.String(), err
		}
		s := string(line)
		for i := 0; i < len(s); i++ {
			if s[i] == '<' && i+1 < len(s) {
				if s[i+1] == '/' {
					depth--
				} else if s[i+1] != '!' && s[i+1] != '?' {
					depth++
				}
			}
			if s[i] == '/' && i+1 < len(s) && s[i+1] == '>' && depth > 0 {
				depth--
			}
		}
		buf.WriteString(s)
		if depth <= 0 {
			break
		}
	}
	return strings.TrimSpace(buf.String()), nil
}

func (s *Server) dispatch(request string) string {
	req := strings.TrimSpace(request)

	if strings.HasPrefix(req, "<auth1") {
		return s.handleAuth1()
	}
	if strings.HasPrefix(req, "<auth2") {
		return s.handleAuth2(req)
	}
	if strings.HasPrefix(req, "<exchange_versions") {
		return cxml.WrapReply(`<server_version><major>1</major><minor>0</minor><release>0</release></server_version>`)
	}
	if strings.HasPrefix(req, "<get_state") {
		return s.wrapCall(s.handler.GetState())
	}
	if strings.HasPrefix(req, "<get_cc_status") {
		return s.wrapCall(s.handler.GetCcStatus())
	}
	if strings.HasPrefix(req, "<get_messages") {
		after := -1
		if m := regexp.MustCompile(`(?s)<seqno>(\d+)</seqno>`).FindStringSubmatch(req); m != nil {
			fmt.Sscanf(m[1], "%d", &after)
		}
		return s.wrapCall(s.handler.GetMessages(after))
	}
	if strings.HasPrefix(req, "<get_file_transfers") {
		return s.wrapCall(s.handler.GetTransfers())
	}
	if strings.HasPrefix(req, "<get_statistics") {
		return s.wrapCall(s.handler.GetStats())
	}
	if strings.HasPrefix(req, "<get_disk_usage") {
		return s.wrapCall(s.handler.GetDiskUsage())
	}
	if strings.HasPrefix(req, "<get_daily_xfer") {
		return s.wrapCall(s.handler.GetDailyXferHistory())
	}
	if strings.HasPrefix(req, "<get_global_prefs_override") {
		return s.wrapCall(s.handler.GetPrefsOverride())
	}
	if strings.HasPrefix(req, "<set_global_prefs_override") {
		pairs := parsePrefsPairs(req)
		if err := s.handler.SetPrefsOverride(pairs); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<set_run_mode") {
		if err := s.handler.SetRunMode(extractTag(req, "mode")); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<set_network_mode") {
		if err := s.handler.SetNetworkMode(extractTag(req, "mode")); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<result_op") {
		if err := s.handler.ResultOp(extractTag(req, "name"), extractTag(req, "operation")); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<project_op") {
		if err := s.handler.ProjectOp(extractTag(req, "project_url"), extractTag(req, "operation")); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<file_transfer_op") {
		if err := s.handler.FileTransferOp(extractTag(req, "ft_name"), extractTag(req, "operation")); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<project_attach") {
		if err := s.handler.ProjectAttach(extractTag(req, "project_url"), extractTag(req, "authenticator"), extractTag(req, "project_name")); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<run_benchmarks") {
		if err := s.handler.RunBenchmarks(); err != nil {
			return cxml.WrapError(err.Error())
		}
		return cxml.WrapSuccess()
	}
	if strings.HasPrefix(req, "<get_host_info") {
		return s.wrapCall(s.handler.GetHostInfo())
	}
	if strings.HasPrefix(req, "<quit") {
		var fn func()
		s.mu.Lock()
		fn = s.onQuit
		s.mu.Unlock()
		if fn != nil {
			go fn()
		}
		return cxml.WrapSuccess()
	}

	return cxml.WrapError("unknown command")
}

var (
	nonceMu sync.Mutex
	nonces  = map[string]time.Time{}
)

func (s *Server) handleAuth1() string {
	nonce := fmt.Sprintf("%x", md5.Sum([]byte(fmt.Sprintf("%d", time.Now().UnixNano()))))
	nonceMu.Lock()
	nonces[nonce] = time.Now()
	nonceMu.Unlock()
	return cxml.WrapReply(fmt.Sprintf("<nonce>%s</nonce>", nonce))
}

func (s *Server) handleAuth2(req string) string {
	m := regexp.MustCompile(`(?s)<nonce_hash>(.*?)</nonce_hash>`).FindStringSubmatch(req)
	if m == nil {
		return cxml.WrapError("no nonce hash")
	}
	nonceMu.Lock()
	defer nonceMu.Unlock()
	for nonce, t := range nonces {
		if time.Since(t) > 5*time.Minute {
			delete(nonces, nonce)
			continue
		}
		sum := md5.Sum(append([]byte(nonce), []byte(s.password)...))
		if m[1] == hex.EncodeToString(sum[:]) {
			delete(nonces, nonce)
			return cxml.WrapReply("<authorized/>")
		}
	}
	return cxml.WrapError("password rejected")
}

func (s *Server) wrapCall(data []byte, err error) string {
	if err != nil {
		return cxml.WrapError(err.Error())
	}
	return cxml.WrapReply(string(data))
}

func extractTag(xml, tag string) string {
	re := regexp.MustCompile(fmt.Sprintf(`(?s)<%s>(.*?)</%s>`, tag, tag))
	if m := re.FindStringSubmatch(xml); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func parsePrefsPairs(x string) [][2]string {
	var pairs [][2]string
	dec := xml.NewDecoder(strings.NewReader(x))
	inOverride := false
	var curKey string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "global_prefs_override" {
				inOverride = true
			} else if inOverride {
				curKey = t.Name.Local
			}
		case xml.CharData:
			if inOverride && curKey != "" {
				val := strings.TrimSpace(string(t))
				if val != "" {
					pairs = append(pairs, [2]string{curKey, val})
				}
			}
		case xml.EndElement:
			if t.Name.Local == "global_prefs_override" {
				inOverride = false
			}
			curKey = ""
		}
	}
	return pairs
}
