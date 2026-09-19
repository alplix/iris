package boinc

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const ETX = 0x03

var ErrNotConnected = errors.New("not connected")

type RPCError struct{ msg string }

func (e *RPCError) Error() string { return e.msg }

func rpcErr(format string, a ...any) *RPCError {
	return &RPCError{msg: fmt.Sprintf(format, a...)}
}

type Client struct {
	host     string
	port     int
	password string
	timeout  time.Duration

	mu        sync.Mutex
	conn      net.Conn
	connected bool
	Version   string
}

func NewClient(host string, port int, password string) *Client {
	if port <= 0 {
		port = 31416
	}
	return &Client{host: host, port: port, password: password, timeout: 12 * time.Second}
}

func (c *Client) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
}

func (c *Client) closeLocked() {
	if c.conn != nil {
		c.conn.Close()
	}
	c.conn = nil
	c.connected = false
}

func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		return nil
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, strconv.Itoa(c.port)), c.timeout)
	if err != nil {
		return rpcErr("connection failed (%s:%d)", c.host, c.port)
	}
	if err := c.handshake(conn); err != nil {
		conn.Close()
		return err
	}
	c.conn = conn
	c.connected = true
	return nil
}

func readFrame(conn net.Conn) ([]byte, error) {
	r := bufio.NewReaderSize(conn, 64*1024)
	var out bytes.Buffer
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && out.Len() > 0 {
				return out.Bytes(), nil
			}
			return nil, err
		}
		if b == ETX {
			return out.Bytes(), nil
		}
		out.WriteByte(b)
	}
}

func (c *Client) roundTrip(conn net.Conn, payload string) ([]byte, error) {
	conn.SetDeadline(time.Now().Add(c.timeout))
	if _, err := conn.Write([]byte(payload + "\n")); err != nil {
		return nil, err
	}
	frame, err := readFrame(conn)
	if err != nil {
		return nil, err
	}
	return frame, nil
}

func (c *Client) handshake(conn net.Conn) error {
	r1, err := c.roundTrip(conn, "<auth1/>")
	if err != nil {
		return rpcErr("handshake failed")
	}
	if !bytes.Contains(r1, []byte("<authorized")) {
		re := regexp.MustCompile(`(?s)<nonce>(.*?)</nonce>`)
		m := re.FindSubmatch(r1)
		if m == nil {
			return rpcErr("no authorization nonce")
		}
		sum := md5.Sum(append(m[1], []byte(c.password)...))
		hash := hex.EncodeToString(sum[:])
		r2, err := c.roundTrip(conn, "<auth2>\n <nonce_hash>"+hash+"</nonce_hash>\n</auth2>")
		if err != nil {
			return rpcErr("handshake failed")
		}
		if !bytes.Contains(r2, []byte("<authorized")) {
			return rpcErr("password rejected - check gui_rpc_auth.cfg on the client")
		}
	}
	frame, err := c.roundTrip(conn, "<exchange_versions>\n  <major>1</major>\n  <minor>0</minor>\n  <release>0</release>\n</exchange_versions>")
	if err == nil {
		v, perr := ParseVersions(frame)
		if perr == nil {
			c.Version = v
		}
	}
	return nil
}

func (c *Client) Call(xml string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		if err := c.connectLocked(); err != nil {
			return nil, err
		}
	}
	frame, err := c.roundTrip(c.conn, xml)
	if err != nil {
		c.closeLocked()
		return nil, rpcErr("request failed (%s:%d)", c.host, c.port)
	}
	return frame, nil
}

func (c *Client) connectLocked() error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.host, strconv.Itoa(c.port)), c.timeout)
	if err != nil {
		return rpcErr("connection failed (%s:%d)", c.host, c.port)
	}
	if err := c.handshake(conn); err != nil {
		conn.Close()
		return err
	}
	c.conn = conn
	c.connected = true
	return nil
}
