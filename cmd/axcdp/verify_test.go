package main

import (
	"strings"
	"testing"
)

func TestVerifyCDPTargetRequiresSelector(t *testing.T) {
	if err := verifyCDPTarget("http://127.0.0.1:1", ""); err == nil {
		t.Fatal("verifyCDPTarget succeeded without -target selector")
	}
}

func TestFindTargetNoMatchListsCandidates(t *testing.T) {
	targets := []map[string]any{
		{"type": "page", "id": "target-1", "title": "TextEdit", "url": "http://127.0.0.1:9221/axcdp/window/1/2"},
		{"type": "node", "id": "node-1", "title": "Node", "url": "http://127.0.0.1:9221/axcdp/node"},
	}
	_, err := findTarget(targets, "Calculator")
	if err == nil {
		t.Fatal("findTarget succeeded for missing selector")
	}
	if !strings.Contains(err.Error(), "available page targets:") || !strings.Contains(err.Error(), "TextEdit") || strings.Contains(err.Error(), "Node") {
		t.Fatalf("findTarget error = %q, want page candidates only", err)
	}
}

func TestVerifyDOMHasWindowChildrenAcceptsWindowDocument(t *testing.T) {
	result := map[string]any{
		"root": map[string]any{
			"nodeName": "#document",
			"children": []any{
				map[string]any{
					"nodeName":       "AXWindow",
					"childNodeCount": 2,
				},
			},
		},
	}
	if err := verifyDOMHasWindowChildren(result); err != nil {
		t.Fatalf("verifyDOMHasWindowChildren: %v", err)
	}
}

func TestVerifyDOMHasWindowChildrenAcceptsApplicationDocument(t *testing.T) {
	result := map[string]any{
		"root": map[string]any{
			"nodeName": "#document",
			"children": []any{
				map[string]any{
					"nodeName": "AXApplication",
					"children": []any{
						map[string]any{
							"nodeName":       "AXWindow",
							"childNodeCount": 1,
						},
					},
				},
			},
		},
	}
	if err := verifyDOMHasWindowChildren(result); err != nil {
		t.Fatalf("verifyDOMHasWindowChildren: %v", err)
	}
}

func TestVerifyDOMHasWindowChildrenRejectsEmptyWindow(t *testing.T) {
	result := map[string]any{
		"root": map[string]any{
			"nodeName": "#document",
			"children": []any{
				map[string]any{
					"nodeName":       "AXWindow",
					"childNodeCount": 0,
				},
			},
		},
	}
	if err := verifyDOMHasWindowChildren(result); err == nil {
		t.Fatal("verifyDOMHasWindowChildren succeeded for empty AXWindow")
	}
}
