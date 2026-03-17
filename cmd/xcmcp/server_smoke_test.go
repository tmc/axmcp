package main

import "testing"

func TestServerSmoke(t *testing.T) {
	srv := startTestServer(t)
	srv.initialize()

	resp := srv.request("tools/list", nil)
	var toolsResult struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	toolsResult = decodeResult[struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, resp)

	expectedTools := []string{
		"build",
		"test",
		"discover_projects",
		"list_schemes",
		"show_build_settings",
		"show_build_products",
		"run_built_app",
		"wait_for_app_ready",
		"render_all_previews",
		"list_simulators",
		"boot_simulator",
		"shutdown_simulator",
	}
	foundTools := map[string]bool{}
	for _, tool := range toolsResult.Tools {
		foundTools[tool.Name] = true
	}
	for _, name := range expectedTools {
		if !foundTools[name] {
			t.Errorf("tool %q not found", name)
		}
	}

	resp = srv.request("resources/list", nil)
	var resourcesResult struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}
	resourcesResult = decodeResult[struct {
		Resources []struct {
			URI string `json:"uri"`
		} `json:"resources"`
	}](t, resp)

	expectedResources := map[string]bool{
		"xcmcp://project":    false,
		"xcmcp://simulators": false,
	}
	for _, resource := range resourcesResult.Resources {
		if _, ok := expectedResources[resource.URI]; ok {
			expectedResources[resource.URI] = true
		}
	}
	for uri, found := range expectedResources {
		if !found {
			t.Errorf("resource %q not found", uri)
		}
	}

	resp = srv.request("tools/call", map[string]interface{}{
		"name":      "list_simulators",
		"arguments": map[string]string{},
	})
	if resp.Error != nil {
		t.Fatalf("list_simulators failed: %v", resp.Error)
	}

	resp = srv.request("resources/read", map[string]interface{}{
		"uri": "xcmcp://simulators",
	})
	if resp.Error != nil {
		t.Fatalf("read simulators resource failed: %v", resp.Error)
	}

	resp = srv.request("tools/call", map[string]interface{}{
		"name": "discover_projects",
		"arguments": map[string]string{
			"path": ".",
		},
	})
	if resp.Error != nil {
		t.Fatalf("discover_projects failed: %v", resp.Error)
	}
}
