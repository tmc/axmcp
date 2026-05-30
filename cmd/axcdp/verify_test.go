package main

import "testing"

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
