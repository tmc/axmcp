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
