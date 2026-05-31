// Package cdp implements the small Chrome DevTools Protocol subset needed by
// computer-use fallbacks.
package cdp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Target describes a debuggable CDP target.
type Target struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// EvalResult is the decoded result of Runtime.evaluate.
type EvalResult struct {
	Type        string `json:"type,omitempty"`
	Value       any    `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
}

// EvaluateOptions controls Electron/Chromium CDP evaluation.
type EvaluateOptions struct {
	PID     int
	Port    int
	Script  string
	Timeout time.Duration
}

// Evaluate sends Runtime.evaluate to a local CDP target. When PID is non-zero,
// SIGUSR1 is sent first, which asks Electron/Node runtimes to start their
// inspector on the default port.
func Evaluate(ctx context.Context, opts EvaluateOptions) (EvalResult, error) {
	if strings.TrimSpace(opts.Script) == "" {
		return EvalResult{}, fmt.Errorf("script is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.PID > 0 {
		_ = startInspector(opts.PID)
	}
	target, err := waitForTarget(ctx, opts)
	if err != nil {
		return EvalResult{}, err
	}
	if target.WebSocketDebuggerURL == "" {
		return EvalResult{}, fmt.Errorf("cdp target has no websocket debugger URL")
	}
	return runtimeEvaluate(ctx, target.WebSocketDebuggerURL, opts.Script)
}

func waitForTarget(ctx context.Context, opts EvaluateOptions) (Target, error) {
	deadline := time.Now().Add(opts.Timeout)
	var last error
	for {
		ports := probePorts(opts.Port)
		for _, port := range ports {
			target, err := firstTarget(ctx, port)
			if err == nil {
				return target, nil
			}
			last = err
		}
		if time.Now().After(deadline) {
			if last != nil {
				return Target{}, last
			}
			return Target{}, fmt.Errorf("cdp target not found")
		}
		select {
		case <-ctx.Done():
			return Target{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func probePorts(port int) []int {
	if port > 0 {
		return []int{port}
	}
	ports := make([]int, 0, 11)
	for p := 9229; p <= 9239; p++ {
		ports = append(ports, p)
	}
	return ports
}

func firstTarget(ctx context.Context, port int) (Target, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/json/list", nil)
	if err != nil {
		return Target{}, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return Target{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Target{}, fmt.Errorf("cdp /json/list status %s", res.Status)
	}
	var targets []Target
	if err := json.NewDecoder(res.Body).Decode(&targets); err != nil {
		return Target{}, fmt.Errorf("decode cdp targets: %w", err)
	}
	for _, target := range targets {
		if target.WebSocketDebuggerURL != "" {
			return target, nil
		}
	}
	return Target{}, fmt.Errorf("cdp target not found on port %d", port)
}

func runtimeEvaluate(ctx context.Context, rawURL, script string) (EvalResult, error) {
	conn, err := dialWebSocket(ctx, rawURL)
	if err != nil {
		return EvalResult{}, err
	}
	defer conn.Close()
	req := map[string]any{
		"id":     1,
		"method": "Runtime.evaluate",
		"params": map[string]any{
			"expression":    script,
			"returnByValue": true,
			"awaitPromise":  true,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return EvalResult{}, err
	}
	if err := writeClientTextFrame(conn, data); err != nil {
		return EvalResult{}, err
	}
	for {
		payload, err := readServerFrame(conn)
		if err != nil {
			return EvalResult{}, err
		}
		var msg struct {
			ID     int `json:"id"`
			Result struct {
				Result    EvalResult      `json:"result"`
				Exception json.RawMessage `json:"exceptionDetails,omitempty"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(payload, &msg); err != nil {
			return EvalResult{}, err
		}
		if msg.ID != 1 {
			continue
		}
		if msg.Error != nil {
			return EvalResult{}, fmt.Errorf("cdp Runtime.evaluate: %s", msg.Error.Message)
		}
		if len(msg.Result.Exception) > 0 {
			return EvalResult{}, fmt.Errorf("cdp Runtime.evaluate exception: %s", msg.Result.Exception)
		}
		return msg.Result.Result, nil
	}
}

func dialWebSocket(ctx context.Context, rawURL string) (net.Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	key, err := websocketKey()
	if err != nil {
		conn.Close()
		return nil, err
	}
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, u.Host, key)
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !strings.Contains(status, " 101 ") {
		conn.Close()
		return nil, fmt.Errorf("websocket upgrade failed: %s", strings.TrimSpace(status))
	}
	var accept string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(value)
		}
	}
	if accept != websocketAccept(key) {
		conn.Close()
		return nil, fmt.Errorf("websocket accept mismatch")
	}
	if br.Buffered() == 0 {
		return conn, nil
	}
	return bufferedConn{Conn: conn, r: br}, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func websocketKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func writeClientTextFrame(w io.Writer, payload []byte) error {
	var buf bytes.Buffer
	buf.WriteByte(0x81)
	writeFrameLength(&buf, len(payload), true)
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	buf.Write(mask[:])
	for i, b := range payload {
		buf.WriteByte(b ^ mask[i%4])
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func readServerFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	opcode := hdr[0] & 0x0f
	if opcode == 0x8 {
		return nil, io.EOF
	}
	if opcode != 0x1 {
		return nil, fmt.Errorf("unexpected websocket opcode %d", opcode)
	}
	masked := hdr[1]&0x80 != 0
	n, err := readFrameLength(r, hdr[1]&0x7f)
	if err != nil {
		return nil, err
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return payload, nil
}

func writeFrameLength(w io.Writer, n int, masked bool) {
	flag := byte(0)
	if masked {
		flag = 0x80
	}
	switch {
	case n < 126:
		w.Write([]byte{flag | byte(n)})
	case n <= 65535:
		w.Write([]byte{flag | 126, byte(n >> 8), byte(n)})
	default:
		var b [9]byte
		b[0] = flag | 127
		binary.BigEndian.PutUint64(b[1:], uint64(n))
		w.Write(b[:])
	}
}

func readFrameLength(r io.Reader, first byte) (int, error) {
	switch first {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		return int(binary.BigEndian.Uint16(b[:])), nil
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		n := binary.BigEndian.Uint64(b[:])
		if n > uint64(^uint(0)>>1) {
			return 0, fmt.Errorf("websocket frame too large")
		}
		return int(n), nil
	default:
		return int(first), nil
	}
}
