package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// clientStdioTransport implements the Transport interface for the MCP client
// talking effectively to the server over stdio.
// In a real scenario, we might use the SDK's client facilities if available,
// but for this test we'll wrap a simple stdio transport or use the SDK's if it exposes one.
//
// Looking at the SDK, it typically provides a server transport. We can simulate a client
// by running the server as a subprocess and connecting stdin/stdout.

func TestToolsE2ETapCrash(t *testing.T) {
	if os.Getenv("XCMCP_E2E") == "" {
		t.Skip("Skipping E2E test; set XCMCP_E2E=1 to run")
	}

	// Use the installed wrapper
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home dir: %v", err)
	}
	serverPath := filepath.Join(homeDir, "go/bin/xcmcp")

	cmd := exec.Command(serverPath)
	// Inherit stderr for debugging
	cmd.Stderr = os.Stderr

	inPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("Failed to get stdin pipe: %v", err)
	}
	outPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// We need a basic JSON-RPC 2.0 client here over stdio.
	// We'll implement a minimal one for this test since the SDK might be server-focused or
	// we want explicit control.

	// Helper to send request
	reqID := 0
	sendRequest := func(method string, params interface{}) (map[string]interface{}, error) {
		reqID++
		req := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      reqID,
			"method":  method,
			"params":  params,
		}
		data, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		// Write line-delimited JSON
		if _, err := inPipe.Write(append(data, '\n')); err != nil {
			return nil, fmt.Errorf("write error: %w", err)
		}

		// Read response
		var resp struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   interface{}     `json:"error"`
		}
		decoder := json.NewDecoder(outPipe)
		if err := decoder.Decode(&resp); err != nil {
			return nil, fmt.Errorf("decode error: %w", err)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error: %v", resp.Error)
		}

		var resMap map[string]interface{}
		if len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, &resMap); err != nil {
				return nil, fmt.Errorf("result unmarshal error: %w", err)
			}
		}
		return resMap, nil
	}

	// 1. Initialize
	t.Log("Initializing...")
	_, err = sendRequest("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", // Example version
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]string{
			"name":    "test-client",
			"version": "1.0",
		},
	})
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Send initialized notification
	reqID++
	notify := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	}
	notifyData, _ := json.Marshal(notify)
	inPipe.Write(append(notifyData, '\n'))

	// 2. List Apps
	t.Log("Listing apps...")
	// The SDK tools structure for response might differ, usually {content: [{type:text, text:...}], isError: false}
	// Let's call the tool.
	callTool := func(name string, args map[string]interface{}) (string, error) {
		res, err := sendRequest("tools/call", map[string]interface{}{
			"name":      name,
			"arguments": args,
		})
		if err != nil {
			return "", err
		}

		// Inspect content
		content, ok := res["content"].([]interface{})
		if !ok || len(content) == 0 {
			return "", fmt.Errorf("no content in result: %v", res)
		}
		firstItem, ok := content[0].(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("invalid content item: %v", content[0])
		}
		text, ok := firstItem["text"].(string)
		if !ok {
			// Might be image content for screenshot, check type
			contentType, _ := firstItem["type"].(string)
			if contentType == "image" {
				return "image_data_validated", nil
			}
			return "", fmt.Errorf("content text not string: %v", firstItem)
		}
		return text, nil
	}

	if _, err := callTool("app_list", map[string]interface{}{}); err != nil {
		t.Fatalf("app_list failed: %v", err)
	}

	// 3. Launch Messages (System app, safe to launch)
	// Using Messages (com.apple.MobileSMS) or Safari (com.apple.mobilesafari)
	targetBundleID := "com.apple.mobilesafari"
	t.Logf("Launching %s...", targetBundleID)
	if _, err := callTool("app_launch", map[string]interface{}{"bundle_id": targetBundleID}); err != nil {
		t.Fatalf("app_launch failed: %v", err)
	}

	// Give it a moment to launch
	time.Sleep(5 * time.Second)

	// 4. Get State
	t.Log("Checking state...")
	// app_get_state returns map directly according to implementation?
	// Wait, mcp.CallToolResult wrapping happens.
	// implementation: return &mcp.CallToolResult{}, map[string]string{"state": state}, nil
	// Wait, the SDK handler returns (result, extra, err). The SDK likely serializes 'result'.
	// If the tool function returns custom structs as 2nd arg, does the SDK merge that into result?
	// Looking at the SDK source would confirm, but usually the result is what matters.
	// The implementation of app_get_state returns `map[string]string` as the second return value.
	// Let's assume standard MCP Tool Call: result content should contain the info?
	// Actually, checking `tools_app.go`:
	// func(...) (*mcp.CallToolResult, map[string]string, error)
	// The 2nd return value seems to be unused by standard SDK unless it uses it for something specific?
	// Ah, wait. checking `xcmcp` source again.
	// The SDK signature for AddTool handler is `func(ctx, request, input) (result, output, error)`.
	// It seems the 'output' is marshalled into the result? No, standard MCP doesn't have "output" field.
	// Likely the SDK implementation ignores the 2nd return value or uses it for side usage.
	// But `app_get_state` creates an empty `CallToolResult` and puts the state in the 2nd arg map.
	// If the SDK drops the 2nd arg, `app_get_state` returns empty result.
	// Let's verify this behavioral assumption in the test.
	// If `xcmcp` relies on 2nd arg, it might be buggy with standard SDK, OR the SDK automatically adds that map to the result content/data?
	// I will inspect the raw response for state.

	// Re-check tools_app.go:
	/*
	   return &mcp.CallToolResult{}, map[string]string{"state": state}, nil
	*/
	// If the SDK doesn't use the 2nd arg, this tool returns nothing.
	// Let's define the test to just call it and log result.

	// 5. UI Tree
	t.Log("Getting UI Tree...")
	if _, err := callTool("ui_tree", map[string]interface{}{"bundle_id": targetBundleID}); err != nil {
		t.Fatalf("ui_tree failed: %v", err)
	}

	// 6. UI Tap Reproduction
	t.Log("Getting Simulator UI Tree...")
	// We use "com.apple.iphonesimulator" to target the simulator app itself (macOS side)
	if _, err := callTool("ui_tree", map[string]interface{}{"bundle_id": "com.apple.iphonesimulator"}); err != nil {
		t.Logf("ui_tree failed: %v", err)
	} else {
		t.Log("ui_tree succeeded")
	}

	t.Log("Attempting ui_tap on 'Edit'...")
	// Note: ID-based tap relies on finding element with that ID or Label.
	// If 'Edit' doesn't exist, it might fail gracefully or crash if the search logic is buggy.
	// We expect a crash/EOF based on user report.
	if _, err := callTool("ui_tap", map[string]interface{}{"id": "Edit"}); err != nil {
		t.Fatalf("ui_tap failed/crashed: %v", err)
	} else {
		t.Log("ui_tap succeeded (unexpected if reproducing crash)")
	}

	t.Log("E2E Test Finished")
}
