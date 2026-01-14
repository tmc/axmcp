package resources

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/project"
	"github.com/tmc/xcmcp/simctl"
	"github.com/tmc/xcmcp/ui"
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

func registerProjectResource(s *mcp.Server, ctx *Context) {
	s.AddResource(&mcp.Resource{
		URI:         "xcmcp://project",
		Name:        "project",
		Description: "Current project metadata",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if ctx.Value("root") == nil {
			// fallback/check logic can go here
		}
		// In a real implementation we would use the Context struct passed to Register
		// For now we will discover on demand from the configured root or "."
		root := "."
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
		Description: "Available iOS simulators",
		MIMEType:    "application/json",
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
		Description: "List of running applications (on simulator)",
		MIMEType:    "application/json",
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
		Description: "The UI hierarchy (accessibility tree) of a running application",
		MIMEType:    "text/plain",
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
		Description: "Recent logs for an application (last 5 min)",
		MIMEType:    "text/plain",
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
