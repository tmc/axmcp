package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/project"
	"github.com/tmc/axmcp/internal/simctl"
	"github.com/tmc/axmcp/internal/ui"
)

// Context holds auto-detected project info
type Context struct {
	ProjectRoot string
}

// Register registers all resources with the MCP server
func Register(s *mcp.Server, ctx *Context) {
	registerProjectResource(s, ctx)
	registerSimulatorsResource(s)
	registerAppsResource(s)
	registerAppResources(s)
}

func registerProjectResource(s *mcp.Server, cfg *Context) {
	s.AddResource(&mcp.Resource{
		URI:         "xcmcp://project",
		Name:        "project",
		Title:       "Project Metadata",
		Description: "Current project metadata",
		MIMEType:    "application/json",
		Annotations: assistantAnnotations(1),
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		root := projectRootForRequest(ctx, req.Session, cfg.ProjectRoot)
		projects, err := project.Discover(root)
		if err != nil {
			return nil, err
		}

		data, err := json.Marshal(projects)
		if err != nil {
			return nil, err
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "xcmcp://project",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}

func registerSimulatorsResource(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		URI:         "xcmcp://simulators",
		Name:        "simulators",
		Title:       "Simulators",
		Description: "Available iOS simulators",
		MIMEType:    "application/json",
		Annotations: assistantAnnotations(0.7),
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		sims, err := simctl.List(ctx)
		if err != nil {
			return nil, err
		}

		data, err := json.Marshal(sims)
		if err != nil {
			return nil, err
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "xcmcp://simulators",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}

func registerAppsResource(s *mcp.Server) {
	s.AddResource(&mcp.Resource{
		URI:         "xcmcp://apps",
		Name:        "apps",
		Title:       "Running Apps",
		Description: "List of running applications (on simulator)",
		MIMEType:    "application/json",
		Annotations: assistantAnnotations(0.5),
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Use simctl to list running apps on booted sim
		out, err := simctl.ListRunningApps(ctx, "booted")
		if err != nil {
			return nil, err
		}
		// Output is text, wrap in JSON string for now or parse it if possible.
		// simctl.ListRunningApps returns []string.
		data, _ := json.Marshal(map[string][]string{"apps": out})

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      "xcmcp://apps",
					MIMEType: "application/json",
					Text:     string(data),
				},
			},
		}, nil
	})
}

func registerAppResources(s *mcp.Server) {
	// Template for UI Tree: xcmcp://apps/{bundle_id}/tree
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "xcmcp://apps/{bundle_id}/tree",
		Name:        "app_ui_tree",
		Title:       "App UI Tree",
		Description: "The UI hierarchy (accessibility tree) of a running application",
		MIMEType:    "text/plain",
		Annotations: assistantAnnotations(0.8),
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Parse bundle_id from URI.
		// Go SDK might not provide auto-parsing yet, need manual extraction or usage of "variables"?
		// The req.URI contains the actual URI.
		// Simple string manipulation for now.
		// Expected: xcmcp://apps/com.example.app/tree
		// Length of prefix "xcmcp://apps/" is 13. Suffix "/tree" is 5.
		uri := req.Params.URI
		if len(uri) < 18 {
			return nil, fmt.Errorf("invalid URI format")
		}
		bundleID := uri[13 : len(uri)-5]

		app := ui.ApplicationWithBundleID(bundleID)
		tree := app.Tree()

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/plain",
					Text:     tree,
				},
			},
		}, nil
	})

	// Template for Logs: xcmcp://apps/{bundle_id}/logs
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "xcmcp://apps/{bundle_id}/logs",
		Name:        "app_logs",
		Title:       "App Logs",
		Description: "Recent logs for an application (last 5 min)",
		MIMEType:    "text/plain",
		Annotations: assistantAnnotations(0.4),
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		// Expected: xcmcp://apps/com.example.app/logs
		// Suffix "/logs" is 5.
		uri := req.Params.URI
		if len(uri) < 18 {
			return nil, fmt.Errorf("invalid URI format")
		}
		bundleID := uri[13 : len(uri)-5]

		logs, err := simctl.GetAppLogs(ctx, "booted", bundleID, "5m")
		if err != nil {
			return nil, err
		}

		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/plain",
					Text:     logs,
				},
			},
		}, nil
	})
}

func assistantAnnotations(priority float64) *mcp.Annotations {
	return &mcp.Annotations{
		Audience: []mcp.Role{"assistant"},
		Priority: priority,
	}
}

func projectRootForRequest(ctx context.Context, session *mcp.ServerSession, fallback string) string {
	roots := sessionFileRoots(ctx, session)
	if len(roots) > 0 {
		return roots[0]
	}
	if fallback == "" {
		return "."
	}
	return fallback
}

func sessionFileRoots(ctx context.Context, session *mcp.ServerSession) []string {
	if session == nil {
		return nil
	}
	rootCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()

	result, err := session.ListRoots(rootCtx, nil)
	if err != nil || result == nil {
		return nil
	}
	var roots []string
	for _, root := range result.Roots {
		if root == nil {
			continue
		}
		u, err := url.Parse(root.URI)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		roots = append(roots, filepath.Clean(u.Path))
	}
	sort.Strings(roots)
	return roots
}
