//go:build darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSwiftUIPreviewPrompt(t *testing.T) {
	ctx := context.Background()
	path := writeSwiftUIViewFile(t)

	server := newPreviewTestServer()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if !hasPrompt(prompts.Prompts, swiftUIPreviewPromptName) {
		t.Fatalf("prompt %q not found", swiftUIPreviewPromptName)
	}

	prompt, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: swiftUIPreviewPromptName,
		Arguments: map[string]string{
			"path":     path,
			"notes":    "use an iPhone-sized layout",
			"variants": "loading, error",
		},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}

	if len(prompt.Messages) != 2 {
		t.Fatalf("len(prompt.Messages) = %d, want 2", len(prompt.Messages))
	}

	instructions, ok := prompt.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("prompt.Messages[0].Content = %T, want *mcp.TextContent", prompt.Messages[0].Content)
	}
	if !strings.Contains(instructions.Text, "GreetingView") {
		t.Fatalf("prompt instructions %q do not mention inferred type", instructions.Text)
	}
	if !strings.Contains(instructions.Text, "iPhone-sized layout") {
		t.Fatalf("prompt instructions %q do not include notes", instructions.Text)
	}
	if !strings.Contains(instructions.Text, "loading, error") {
		t.Fatalf("prompt instructions %q do not include variants", instructions.Text)
	}

	resource, ok := prompt.Messages[1].Content.(*mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("prompt.Messages[1].Content = %T, want *mcp.EmbeddedResource", prompt.Messages[1].Content)
	}
	if resource.Resource == nil {
		t.Fatal("embedded resource is nil")
	}
	if resource.Resource.MIMEType != "text/x-swift" {
		t.Fatalf("embedded resource mime = %q, want %q", resource.Resource.MIMEType, "text/x-swift")
	}
	if !strings.Contains(resource.Resource.Text, "struct GreetingView: View") {
		t.Fatalf("embedded resource text missing source: %q", resource.Resource.Text)
	}
}

func newPreviewTestServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "xcmcp", Version: "test"}, nil)
	registerSwiftUIPreviewFeatures(server)
	return server
}

func writeSwiftUIViewFile(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "GreetingView.swift")
	const source = `import SwiftUI

struct GreetingView: View {
	var body: some View {
		Text("Hello")
	}
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func hasPrompt(prompts []*mcp.Prompt, name string) bool {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return true
		}
	}
	return false
}
