package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The bridge cache stores the tool schemas advertised by xcrun mcpbridge so
// that xcmcp can answer tools/list without starting the bridge. Starting the
// bridge costs a subprocess, an Xcode permission dialog, and several seconds;
// most sessions never call an Xcode tool at all.
//
// The cache is keyed by a fingerprint of the active Xcode installation. A
// mismatch discards the cache and falls back to live discovery.

// bridgeCache is the on-disk cache format.
type bridgeCache struct {
	Fingerprint string      `json:"fingerprint"`
	Tools       []*mcp.Tool `json:"tools"`
}

// bridgeCachePath returns the cache file location.
func bridgeCachePath() string {
	return filepath.Join(os.Getenv("HOME"), ".xcmcp", "bridge-tools.json")
}

// developerDir reports the active Xcode developer directory without shelling
// out to xcode-select.
func developerDir() string {
	if d := os.Getenv("DEVELOPER_DIR"); d != "" {
		return d
	}
	if link, err := os.Readlink("/var/db/xcode_select_link"); err == nil {
		return link
	}
	return "/Applications/Xcode.app/Contents/Developer"
}

// xcodeFingerprint identifies the active Xcode installation. It combines the
// developer directory with the size and modification time of the enclosing
// bundle's Info.plist, which changes on every Xcode upgrade.
func xcodeFingerprint() (string, error) {
	dir := developerDir()
	plist := filepath.Join(dir, "..", "Info.plist")
	fi, err := os.Stat(plist)
	if err != nil {
		return "", fmt.Errorf("stat xcode info.plist: %w", err)
	}
	return fmt.Sprintf("%s|%d|%d", dir, fi.Size(), fi.ModTime().UnixNano()), nil
}

// loadBridgeCache returns the cached tools for the active Xcode, or nil if the
// cache is missing, unreadable, or stale.
func loadBridgeCache() []*mcp.Tool {
	want, err := xcodeFingerprint()
	if err != nil {
		slog.Debug("bridge cache: no fingerprint", "err", err)
		return nil
	}
	data, err := os.ReadFile(bridgeCachePath())
	if err != nil {
		return nil
	}
	var c bridgeCache
	if err := json.Unmarshal(data, &c); err != nil {
		slog.Debug("bridge cache: unreadable, ignoring", "err", err)
		return nil
	}
	if c.Fingerprint != want {
		slog.Debug("bridge cache: stale, ignoring")
		return nil
	}
	if len(c.Tools) == 0 {
		return nil
	}
	return c.Tools
}

// saveBridgeCache records tools for the active Xcode. Cache write failures are
// not fatal: the next start simply rediscovers from the bridge.
func saveBridgeCache(tools []*mcp.Tool) error {
	if len(tools) == 0 {
		return nil
	}
	fp, err := xcodeFingerprint()
	if err != nil {
		return err
	}
	data, err := json.Marshal(bridgeCache{Fingerprint: fp, Tools: tools})
	if err != nil {
		return fmt.Errorf("marshal bridge cache: %w", err)
	}
	path := bridgeCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	// Write via a temporary file so concurrent xcmcp instances never observe
	// a partially written cache.
	tmp, err := os.CreateTemp(filepath.Dir(path), "bridge-tools-*.json")
	if err != nil {
		return fmt.Errorf("create cache temp: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write cache temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close cache temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("install cache: %w", err)
	}
	return nil
}
