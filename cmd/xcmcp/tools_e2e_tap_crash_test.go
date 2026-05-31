//go:build darwin

package main

import (
	"os"
	"testing"
	"time"
)

func TestToolsE2ETapCrash(t *testing.T) {
	if os.Getenv("XCMCP_E2E") == "" {
		t.Skip("set XCMCP_E2E=1 to run")
	}

	srv := startTestServer(t)
	srv.initialize()

	if resp := srv.callTool("app_list", map[string]interface{}{}); resp.Error != nil {
		t.Fatalf("app_list failed: %v", resp.Error)
	}

	targetBundleID := "com.apple.mobilesafari"
	if resp := srv.callTool("app_launch", map[string]interface{}{"bundle_id": targetBundleID}); resp.Error != nil {
		t.Fatalf("app_launch failed: %v", resp.Error)
	}

	time.Sleep(5 * time.Second)

	if resp := srv.callTool("ui_tree", map[string]interface{}{"bundle_id": targetBundleID}); resp.Error != nil {
		t.Fatalf("ui_tree failed: %v", resp.Error)
	}

	if resp := srv.callTool("ui_tree", map[string]interface{}{"bundle_id": "com.apple.iphonesimulator"}); resp.Error != nil {
		t.Logf("ui_tree failed: %v", resp.Error)
	} else {
		t.Log("ui_tree succeeded")
	}

	if resp := srv.callTool("ui_tap", map[string]interface{}{"id": "Edit"}); resp.Error != nil {
		t.Fatalf("ui_tap failed/crashed: %v", resp.Error)
	} else {
		t.Log("ui_tap succeeded (unexpected if reproducing crash)")
	}
}
