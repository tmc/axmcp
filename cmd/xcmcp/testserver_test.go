package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type testServer struct {
	t       *testing.T
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  int
}

func startTestServer(t *testing.T, args ...string) *testServer {
	t.Helper()

	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "xcmcp-test")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build xcmcp: %v", err)
	}

	args = append([]string{"-wait-for-xcode=0s"}, args...)
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "XCMCP_NO_MACGO=1")
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xcmcp: %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	s := &testServer{
		t:       t,
		cmd:     cmd,
		stdin:   stdin,
		scanner: scanner,
	}
	t.Cleanup(func() {
		_ = s.stdin.Close()
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.cmd.Wait()
	})
	return s
}

func (s *testServer) notify(method string, params interface{}) {
	s.t.Helper()

	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		s.t.Fatalf("marshal notification %s: %v", method, err)
	}
	if _, err := fmt.Fprintf(s.stdin, "%s\n", data); err != nil {
		s.t.Fatalf("write notification %s: %v", method, err)
	}
}

func (s *testServer) request(method string, params interface{}) *rpcResponse {
	s.t.Helper()

	s.nextID++
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      s.nextID,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		s.t.Fatalf("marshal request %s: %v", method, err)
	}
	if _, err := fmt.Fprintf(s.stdin, "%s\n", data); err != nil {
		s.t.Fatalf("write request %s: %v", method, err)
	}

	for s.scanner.Scan() {
		line := s.scanner.Bytes()
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			s.t.Logf("ignoring non-json line: %s", line)
			continue
		}
		if resp.ID == req.ID {
			return &resp
		}
	}
	if err := s.scanner.Err(); err != nil {
		s.t.Fatalf("read response %s: %v", method, err)
	}
	s.t.Fatalf("EOF waiting for response to %s", method)
	return nil
}

func (s *testServer) initialize() {
	s.t.Helper()

	resp := s.request("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "test-client",
			"version": "1.0",
		},
	})
	if resp.Error != nil {
		s.t.Fatalf("initialize failed: %v", resp.Error)
	}
	s.notify("notifications/initialized", map[string]interface{}{})
}

func (s *testServer) callTool(name string, args interface{}) *rpcResponse {
	s.t.Helper()

	return s.request("tools/call", map[string]interface{}{
		"name":      name,
		"arguments": args,
	})
}

func decodeResult[T any](t *testing.T, resp *rpcResponse) T {
	t.Helper()

	if resp.Error != nil {
		t.Fatalf("rpc error: %v", resp.Error)
	}
	var out T
	if len(resp.Result) == 0 {
		return out
	}
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return out
}
