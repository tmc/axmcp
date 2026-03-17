package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type xcodeBridge interface {
	callTool(context.Context, string, map[string]any) (*mcp.CallToolResult, error)
}

var sharedXcodeBridge struct {
	mu     sync.RWMutex
	bridge xcodeBridge
}

func setXcodeBridge(bridge xcodeBridge) {
	sharedXcodeBridge.mu.Lock()
	defer sharedXcodeBridge.mu.Unlock()
	sharedXcodeBridge.bridge = bridge
}

func getXcodeBridge() xcodeBridge {
	sharedXcodeBridge.mu.RLock()
	defer sharedXcodeBridge.mu.RUnlock()
	return sharedXcodeBridge.bridge
}

func decodeBridgeResult[T any](result *mcp.CallToolResult, out *T) error {
	switch {
	case result == nil:
		return fmt.Errorf("empty bridge result")
	case result.StructuredContent != nil:
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	default:
		for _, item := range result.Content {
			text, ok := item.(*mcp.TextContent)
			if !ok || text.Text == "" {
				continue
			}
			if err := json.Unmarshal([]byte(text.Text), out); err == nil {
				return nil
			}
		}
		return fmt.Errorf("bridge result did not contain structured JSON")
	}
}
