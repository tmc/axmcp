package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeXcode creates a stand-in Xcode bundle and points DEVELOPER_DIR at it,
// so fingerprinting does not depend on a real Xcode installation.
func fakeXcode(t *testing.T) (plist string) {
	t.Helper()
	root := t.TempDir()
	contents := filepath.Join(root, "Xcode.app", "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "Developer"), 0755); err != nil {
		t.Fatal(err)
	}
	plist = filepath.Join(contents, "Info.plist")
	if err := os.WriteFile(plist, []byte("<plist/>"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEVELOPER_DIR", filepath.Join(contents, "Developer"))
	t.Setenv("HOME", root)
	return plist
}

func TestBridgeCacheRoundTrip(t *testing.T) {
	fakeXcode(t)

	if got := loadBridgeCache(); got != nil {
		t.Fatalf("loadBridgeCache on empty cache = %v, want nil", got)
	}

	tools := []*mcp.Tool{
		{Name: "build", Description: "build a scheme", InputSchema: map[string]any{"type": "object"}},
		{Name: "test", Description: "run tests", InputSchema: map[string]any{"type": "object"}},
	}
	if err := saveBridgeCache(tools); err != nil {
		t.Fatalf("saveBridgeCache: %v", err)
	}

	got := loadBridgeCache()
	if len(got) != len(tools) {
		t.Fatalf("loadBridgeCache returned %d tools, want %d", len(got), len(tools))
	}
	for i, want := range tools {
		if got[i].Name != want.Name {
			t.Errorf("tool %d name = %q, want %q", i, got[i].Name, want.Name)
		}
		if got[i].Description != want.Description {
			t.Errorf("tool %d description = %q, want %q", i, got[i].Description, want.Description)
		}
	}
}

func TestBridgeCacheStaleAfterXcodeChange(t *testing.T) {
	plist := fakeXcode(t)

	if err := saveBridgeCache([]*mcp.Tool{{Name: "build"}}); err != nil {
		t.Fatalf("saveBridgeCache: %v", err)
	}
	if loadBridgeCache() == nil {
		t.Fatal("cache not readable immediately after save")
	}

	// Simulate an Xcode upgrade: the bundle's Info.plist changes.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(plist, future, future); err != nil {
		t.Fatal(err)
	}
	if got := loadBridgeCache(); got != nil {
		t.Errorf("loadBridgeCache after Xcode change = %v, want nil (stale)", got)
	}
}

func TestSaveBridgeCacheEmptyIsNoop(t *testing.T) {
	fakeXcode(t)

	if err := saveBridgeCache(nil); err != nil {
		t.Errorf("saveBridgeCache(nil) = %v, want nil", err)
	}
	path, err := bridgeCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("saveBridgeCache(nil) created a cache file")
	}
}
