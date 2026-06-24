package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestShouldAttemptXcodeBridge(t *testing.T) {
	tests := []struct {
		name            string
		mcpXcodePID     string
		hasRunningXcode bool
		want            bool
	}{
		{
			name:        "explicit xcode pid",
			mcpXcodePID: "1234",
			want:        true,
		},
		{
			name:            "running xcode",
			hasRunningXcode: true,
			want:            true,
		},
		{
			name: "no xcode available",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldAttemptXcodeBridge(tt.mcpXcodePID, tt.hasRunningXcode)
			if got != tt.want {
				t.Fatalf("shouldAttemptXcodeBridge(%q, %t) = %t, want %t", tt.mcpXcodePID, tt.hasRunningXcode, got, tt.want)
			}
		})
	}
}

func TestBuildErrorPollerStartsOnlyWithSubscriber(t *testing.T) {
	poller := newBuildErrorPoller("xcmcp://xcode/build-errors")
	poller.interval = time.Hour
	poller.fetch = func(context.Context, *xcodeProxy) (string, error) {
		return "", errors.New("unexpected fetch")
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	poller.bind(server, &xcodeProxy{})
	if poller.isRunning() {
		t.Fatal("poller started without subscribers")
	}

	req := &mcp.SubscribeRequest{Params: &mcp.SubscribeParams{URI: poller.uri}}
	if err := poller.subscribe(context.Background(), req); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !poller.isRunning() {
		t.Fatal("poller did not start after subscription")
	}

	unreq := &mcp.UnsubscribeRequest{Params: &mcp.UnsubscribeParams{URI: poller.uri}}
	if err := poller.unsubscribe(context.Background(), unreq); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for poller.isRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if poller.isRunning() {
		t.Fatal("poller did not stop after last unsubscribe")
	}
}

func TestBuildErrorPollerRejectsOtherResources(t *testing.T) {
	poller := newBuildErrorPoller("xcmcp://xcode/build-errors")

	err := poller.subscribe(context.Background(), &mcp.SubscribeRequest{
		Params: &mcp.SubscribeParams{URI: "xcmcp://project"},
	})
	if err == nil {
		t.Fatal("subscribe to unrelated resource succeeded")
	}

	err = poller.unsubscribe(context.Background(), &mcp.UnsubscribeRequest{
		Params: &mcp.UnsubscribeParams{URI: "xcmcp://project"},
	})
	if err == nil {
		t.Fatal("unsubscribe from unrelated resource succeeded")
	}
}

func TestBuildErrorPollerStopsWhenSessionCloses(t *testing.T) {
	poller := newBuildErrorPoller("xcmcp://xcode/build-errors")
	poller.interval = time.Hour
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	poller.bind(server, &xcodeProxy{})

	req := &mcp.SubscribeRequest{Params: &mcp.SubscribeParams{URI: poller.uri}}
	if err := poller.subscribe(context.Background(), req); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if !poller.isRunning() {
		t.Fatal("poller did not start after subscription")
	}

	poller.sessionClosed(req.Session)

	deadline := time.Now().Add(time.Second)
	for poller.isRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if poller.isRunning() {
		t.Fatal("poller did not stop after session close")
	}
}
