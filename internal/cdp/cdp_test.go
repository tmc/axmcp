package cdp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbePorts(t *testing.T) {
	if got := probePorts(9333); len(got) != 1 || got[0] != 9333 {
		t.Fatalf("probePorts(9333) = %v, want [9333]", got)
	}
	got := probePorts(0)
	if len(got) != 11 || got[0] != 9229 || got[len(got)-1] != 9239 {
		t.Fatalf("probePorts(0) = %v, want 9229..9239", got)
	}
}

func TestFirstTarget(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/list" {
			t.Fatalf("path = %q, want /json/list", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]Target{{
			ID:                   "1",
			Type:                 "node",
			WebSocketDebuggerURL: "ws://127.0.0.1/devtools/page/1",
		}})
	}))
	defer ts.Close()

	port := serverPort(t, ts.URL)
	target, err := firstTarget(t.Context(), port)
	if err != nil {
		t.Fatalf("firstTarget: %v", err)
	}
	if target.ID != "1" {
		t.Fatalf("target.ID = %q, want 1", target.ID)
	}
}

func TestWebSocketFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeClientTextFrame(&buf, []byte("hello")); err != nil {
		t.Fatalf("writeClientTextFrame: %v", err)
	}
	data := buf.Bytes()
	if data[0] != 0x81 || data[1]&0x80 == 0 {
		t.Fatalf("client frame header = % x, want text masked frame", data[:2])
	}
	payload, err := readMaskedClientFrame(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("readMaskedClientFrame: %v", err)
	}
	if string(payload) != "hello" {
		t.Fatalf("payload = %q, want hello", payload)
	}

	var server bytes.Buffer
	server.Write([]byte{0x81, 5})
	server.WriteString("world")
	got, err := readServerFrame(&server)
	if err != nil {
		t.Fatalf("readServerFrame: %v", err)
	}
	if string(got) != "world" {
		t.Fatalf("server payload = %q, want world", got)
	}
}

func serverPort(t *testing.T, raw string) int {
	t.Helper()
	i := strings.LastIndex(raw, ":")
	if i < 0 {
		t.Fatalf("server URL has no port: %s", raw)
	}
	var port int
	if _, err := fmt.Sscanf(raw[i+1:], "%d", &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

func readMaskedClientFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n, err := readFrameLength(r, hdr[1]&0x7f)
	if err != nil {
		return nil, err
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}
