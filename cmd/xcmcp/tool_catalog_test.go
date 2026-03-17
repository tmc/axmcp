package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestServerToolSurfaceWithoutXcode(t *testing.T) {
	srv := startTestServer(t, "-enable-xcode-tools=false", "-wait-for-xcode=0s")
	srv.initialize()

	resp := srv.request("tools/list", nil)
	toolsResult := decodeResult[struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, resp)

	got := map[string]bool{}
	for _, tool := range toolsResult.Tools {
		got[tool.Name] = true
	}

	for _, name := range []string{
		"build",
		"test",
		"show_build_products",
		"run_built_app",
		"wait_for_app_ready",
		"list_toolsets",
		"enable_toolset",
	} {
		if !got[name] {
			t.Fatalf("expected core tool %q to be present", name)
		}
	}

	for _, name := range []string{
		"render_all_previews",
		"xcode_add_target",
		"debug_attach",
		"BuildProject",
		"RenderPreview",
	} {
		if got[name] {
			t.Fatalf("unexpected xcode-only tool %q in non-xcode surface", name)
		}
	}
}

func TestStaticToolNamesUnique(t *testing.T) {
	root := "."
	pattern := regexp.MustCompile(`Name:\s+"([A-Za-z0-9_]+)"`)
	seen := map[string]string{}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "tools_") || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			toolName := match[1]
			if prev, ok := seen[toolName]; ok {
				t.Fatalf("duplicate tool name %q in %s and %s", toolName, prev, path)
			}
			seen[toolName] = path
		}
	}
}

func TestToolsetRegistryRejectsDuplicateNames(t *testing.T) {
	r := &toolsetRegistry{enabled: map[string]bool{}}
	r.add(toolset{name: "device"})

	defer func() {
		if recover() == nil {
			t.Fatal("expected duplicate toolset registration to panic")
		}
	}()
	r.add(toolset{name: "device"})
}

func TestDebuggingToolsetEnablement(t *testing.T) {
	srv := startTestServer(t, "-enable-xcode-tools=false", "-wait-for-xcode=0s")
	srv.initialize()

	resp := srv.request("tools/list", nil)
	before := decodeResult[struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, resp)
	for _, tool := range before.Tools {
		if tool.Name == "debug_attach" {
			t.Fatal("debug_attach unexpectedly present before enabling debugging toolset")
		}
	}

	resp = srv.callTool("enable_toolset", map[string]any{"name": "debugging"})
	if resp.Error != nil {
		t.Fatalf("enable_toolset(debugging) failed: %v", resp.Error)
	}

	resp = srv.request("tools/list", nil)
	after := decodeResult[struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, resp)

	got := map[string]bool{}
	for _, tool := range after.Tools {
		got[tool.Name] = true
	}
	for _, name := range []string{
		"debug_attach",
		"debug_list_sessions",
		"debug_command",
		"debug_continue",
		"debug_stack",
		"debug_variables",
		"debug_breakpoint_add",
		"debug_breakpoint_remove",
		"debug_detach",
	} {
		if !got[name] {
			t.Fatalf("expected debugging tool %q after enablement", name)
		}
	}
}
