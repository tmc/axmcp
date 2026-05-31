package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteJSONReportsWriteError(t *testing.T) {
	err := writeJSON(failingWriter{}, response{Result: map[string]any{"ok": true}})
	if err == nil {
		t.Fatal("writeJSON succeeded with failing writer")
	}
	if !strings.Contains(err.Error(), "write json response") || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("writeJSON error = %v, want wrapped write failure", err)
	}
}

func TestParseCommand(t *testing.T) {
	method, params, err := parseCommand(`AX.copyAttributeValue {"element":"element-1","attribute":"AXRole"}`)
	if err != nil {
		t.Fatalf("parseCommand: %v", err)
	}
	if method != "AX.copyAttributeValue" {
		t.Fatalf("method = %q, want AX.copyAttributeValue", method)
	}
	if params["element"] != "element-1" {
		t.Fatalf("element = %v, want element-1", params["element"])
	}
	if params["attribute"] != "AXRole" {
		t.Fatalf("attribute = %v, want AXRole", params["attribute"])
	}
}

func TestParseCommandRejectsInvalidMethod(t *testing.T) {
	if _, _, err := parseCommand(`AX {}`); err == nil {
		t.Fatal("parseCommand succeeded for invalid method")
	}
}

func TestDispatchGetVersion(t *testing.T) {
	s := &server{
		refs:    make(map[string]axuiautomation.AXUIElementRef),
		strings: make(map[string]corefoundation.CFStringRef),
	}
	result, err := s.dispatch("AX.getVersion", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result["version"] != "0.1.0" {
		t.Fatalf("version = %v, want 0.1.0", result["version"])
	}
}
