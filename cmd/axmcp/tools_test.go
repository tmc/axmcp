package main

import (
	"strings"
	"testing"
)

func TestTreeTextIndexedIncludesIndexAndActions(t *testing.T) {
	nodes := []axTreeNode{
		{Index: 0, ParentIndex: -1, Role: "AXWindow", Title: "Browser", Width: 800, Height: 600},
		{Index: 1, ParentIndex: 0, Depth: 1, Role: "AXWebArea", Title: "Page", SecondaryActions: []string{"AXPress"}},
	}

	got := treeTextIndexed(nodes)
	for _, want := range []string{
		`[0] AXWindow title="Browser"`,
		`  [1] AXWebArea title="Page" actions="AXPress"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("treeTextIndexed missing %q in %q", want, got)
		}
	}
}
