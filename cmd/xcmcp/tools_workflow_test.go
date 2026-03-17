package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeBridge struct {
	windows *mcp.CallToolResult
	renders map[string]*mcp.CallToolResult
}

func (f *fakeBridge) callTool(_ context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	switch name {
	case "XcodeListWindows":
		return f.windows, nil
	case "RenderPreview":
		key := args["sourceFilePath"].(string) + "#" + fmt.Sprint(args["previewDefinitionIndexInFile"])
		return f.renders[key], nil
	default:
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "unexpected tool"}}}, nil
	}
}

func TestRenderAllPreviews(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "MeshView.swift")
	if err := os.WriteFile(source, []byte(`import SwiftUI

#Preview("A") { Text("A") }
#Preview("B") { Text("B") }
`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bridge := &fakeBridge{
		windows: &mcp.CallToolResult{
			StructuredContent: map[string]any{
				"windows": []any{
					map[string]any{"tabIdentifier": "workspace-tab-1"},
				},
			},
		},
		renders: map[string]*mcp.CallToolResult{
			"MeshView.swift#0": {Content: []mcp.Content{&mcp.ImageContent{Data: []byte("png-a"), MIMEType: "image/png"}}},
			"MeshView.swift#1": {Content: []mcp.Content{&mcp.ImageContent{Data: []byte("png-b"), MIMEType: "image/png"}}},
		},
	}
	setXcodeBridge(bridge)
	t.Cleanup(func() { setXcodeBridge(nil) })

	out, err := renderAllPreviews(context.Background(), RenderAllPreviewsInput{
		Root: dir,
		Glob: "**/*.swift",
	})
	if err != nil {
		t.Fatalf("renderAllPreviews: %v", err)
	}
	if out.TabIdentifier != "workspace-tab-1" {
		t.Fatalf("TabIdentifier = %q, want workspace-tab-1", out.TabIdentifier)
	}
	if len(out.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(out.Results))
	}
	for _, result := range out.Results {
		if !result.Success {
			t.Fatalf("preview result failed: %+v", result)
		}
		if _, err := os.Stat(result.SnapshotPath); err != nil {
			t.Fatalf("snapshot missing: %v", err)
		}
	}
}

func TestGlobMatch(t *testing.T) {
	if !globMatch("**/*.swift", "Sources/MeshView.swift") {
		t.Fatal("globMatch failed for recursive swift glob")
	}
	if globMatch("**/*.swift", "Sources/MeshView.m") {
		t.Fatal("globMatch matched non-swift file")
	}
}
