package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

func TestComputerUseSpecParity(t *testing.T) {
	ctx := context.Background()
	cs := newTestClientSession(t, ctx)

	initRes := cs.InitializeResult()
	if initRes == nil {
		t.Fatal("InitializeResult() = nil")
	}
	if initRes.Instructions != computerUseInstructions() {
		t.Fatalf("initialize instructions mismatch\n got: %q\nwant: %q", initRes.Instructions, computerUseInstructions())
	}
	if initRes.Capabilities == nil || initRes.Capabilities.Tools == nil {
		t.Fatal("initialize capabilities missing tools")
	}
	if initRes.Capabilities.Tools.ListChanged {
		t.Fatal("tools.listChanged = true, want false")
	}
	if initRes.Capabilities.Resources == nil || !initRes.Capabilities.Resources.ListChanged {
		t.Fatal("resources.listChanged = false, want true")
	}

	got, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := orderedComputerUseTools()
	if !reflect.DeepEqual(normalizeJSON(t, got.Tools), normalizeJSON(t, want)) {
		gotJSON, _ := json.MarshalIndent(normalizeJSON(t, got.Tools), "", "  ")
		wantJSON, _ := json.MarshalIndent(normalizeJSON(t, want), "", "  ")
		t.Fatalf("tools/list mismatch\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

func TestComputerUsePermissionsResource(t *testing.T) {
	ctx := context.Background()
	cs := newTestClientSession(t, ctx)

	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(res.Resources) != 1 || res.Resources[0].URI != "mcp://permissions/status" {
		t.Fatalf("ListResources = %#v, want permissions status resource", res.Resources)
	}
	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "mcp://permissions/status"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(read.Contents) != 1 || !strings.Contains(read.Contents[0].Text, "\"accessibility\"") {
		t.Fatalf("ReadResource contents = %#v, want JSON snapshot", read.Contents)
	}
	if _, err := cs.ListResourceTemplates(ctx, nil); err == nil || !strings.Contains(err.Error(), "Method not found") {
		t.Fatalf("ListResourceTemplates error = %v, want method not found", err)
	}
}

func TestRequiresRefreshResult(t *testing.T) {
	res, payload, err := requiresRefreshResult("click", "Brave")
	if err != nil {
		t.Fatalf("requiresRefreshResult error = %v", err)
	}
	if res == nil || !res.IsError {
		t.Fatalf("requiresRefreshResult result = %#v, want tool error", res)
	}
	action, ok := payload.(computeruse.ActionResult)
	if !ok {
		t.Fatalf("payload type = %T, want ActionResult", payload)
	}
	if !action.RequiresRefresh {
		t.Fatalf("RequiresRefresh = false, want true")
	}
	if !strings.Contains(action.Message, "call get_app_state again") {
		t.Fatalf("Message = %q, want refresh guidance", action.Message)
	}
}

func newTestClientSession(t *testing.T, ctx context.Context) *mcp.ClientSession {
	t.Helper()

	server := newComputerUseServer(&runtimeState{})

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
	})
	return cs
}

func normalizeJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return out
}
