package main

import (
	"testing"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/axuiautomation"
)

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
