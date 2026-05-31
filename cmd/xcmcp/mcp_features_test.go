package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/resources"
)

func TestServerInitializeInstructionsAndCompletions(t *testing.T) {
	server := newProtocolFeatureTestServer(t)
	cs := connectProtocolFeatureClient(t, server)
	defer cs.Close()

	initResult := cs.InitializeResult()
	if initResult == nil {
		t.Fatal("InitializeResult is nil")
	}
	if initResult.Instructions == "" {
		t.Fatal("initialize instructions are empty")
	}
	if initResult.Capabilities == nil || initResult.Capabilities.Completions == nil {
		t.Fatal("completion capability not advertised")
	}
}

func TestProjectResourceUsesClientRoots(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "SampleApp.xcodeproj")
	if err := os.Mkdir(projectDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", projectDir, err)
	}

	server := newProtocolFeatureTestServer(t)
	cs := connectProtocolFeatureClient(t, server, dir)
	defer cs.Close()

	result, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "xcmcp://project"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("len(result.Contents) = %d, want 1", len(result.Contents))
	}

	var projects []struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(result.Contents[0].Text), &projects); err != nil {
		t.Fatalf("Unmarshal project resource: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1", len(projects))
	}
	if projects[0].Path != projectDir {
		t.Fatalf("project path = %q, want %q", projects[0].Path, projectDir)
	}
}

func TestSwiftUIPreviewPromptPathCompletionUsesRoots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "GreetingView.swift")
	if err := os.WriteFile(path, []byte("import SwiftUI\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	server := newProtocolFeatureTestServer(t)
	cs := connectProtocolFeatureClient(t, server, dir)
	defer cs.Close()

	result, err := cs.Complete(context.Background(), &mcp.CompleteParams{
		Argument: mcp.CompleteParamsArgument{
			Name:  "path",
			Value: "Greet",
		},
		Ref: &mcp.CompleteReference{
			Type: "ref/prompt",
			Name: swiftUIPreviewPromptName,
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Completion.Values) == 0 {
		t.Fatal("completion returned no values")
	}
	if result.Completion.Values[0] != path {
		t.Fatalf("completion value = %q, want %q", result.Completion.Values[0], path)
	}
}

func TestSessionRootHelpersUseClientRoots(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "A")
	second := filepath.Join(dir, "B")
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", first, err)
	}
	if err := os.Mkdir(second, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", second, err)
	}

	server := newRootProbeTestServer(t)
	cs := connectProtocolFeatureClient(t, server, second, first)
	defer cs.Close()

	got := callRootProbe(t, cs)
	if got.Root != first {
		t.Fatalf("root = %q, want first sorted root %q", got.Root, first)
	}
	if len(got.Roots) != 2 || got.Roots[0] != first || got.Roots[1] != second {
		t.Fatalf("roots = %#v, want [%q %q]", got.Roots, first, second)
	}
}

func TestSessionRootHelpersUseFallbackWithoutClientRoots(t *testing.T) {
	server := newRootProbeTestServer(t)
	cs := connectProtocolFeatureClient(t, server)
	defer cs.Close()

	got := callRootProbe(t, cs)
	if got.Root != "fallback-root" {
		t.Fatalf("root = %q, want fallback-root", got.Root)
	}
	if len(got.Roots) != 0 {
		t.Fatalf("roots = %#v, want none", got.Roots)
	}
}

func newProtocolFeatureTestServer(t *testing.T) *mcp.Server {
	t.Helper()

	opts := &mcp.ServerOptions{
		Instructions:      serverInstructions(true, ""),
		CompletionHandler: completionHandler,
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "xcmcp", Version: "test"}, opts)
	registerSwiftUIPreviewFeatures(server)
	resources.Register(server, &resources.Context{ProjectRoot: "."})
	return server
}

func newRootProbeTestServer(t *testing.T) *mcp.Server {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "xcmcp", Version: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "root_probe"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
		Root  string   `json:"root"`
		Roots []string `json:"roots"`
	}, error) {
		return &mcp.CallToolResult{}, struct {
			Root  string   `json:"root"`
			Roots []string `json:"roots"`
		}{
			Root:  sessionProjectRoot(ctx, req.Session, "fallback-root"),
			Roots: sessionFileRoots(ctx, req.Session),
		}, nil
	})
	return server
}

func callRootProbe(t *testing.T, cs *mcp.ClientSession) struct {
	Root  string   `json:"root"`
	Roots []string `json:"roots"`
} {
	t.Helper()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "root_probe"})
	if err != nil {
		t.Fatalf("CallTool(root_probe): %v", err)
	}
	var out struct {
		Root  string   `json:"root"`
		Roots []string `json:"roots"`
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("Marshal StructuredContent: %v", err)
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal StructuredContent: %v", err)
	}
	return out
}

func connectProtocolFeatureClient(t *testing.T, server *mcp.Server, roots ...string) *mcp.ClientSession {
	t.Helper()

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	for _, root := range roots {
		client.AddRoots(&mcp.Root{URI: (&url.URL{Scheme: "file", Path: root}).String()})
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return cs
}
