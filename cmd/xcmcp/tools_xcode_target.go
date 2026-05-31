//go:build darwin

package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/apple/x/axuiautomation"

	"github.com/tmc/axmcp/internal/project"
	"github.com/tmc/axmcp/internal/xcodewizard"
)

func registerXcodeTargetTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "xcode_add_target",
		Description: "Add a new target to the current Xcode project by driving the " +
			"File > New > Target wizard via accessibility automation. Known-working " +
			"templates include 'Widget Extension' and 'App Intent Extension'. " +
			"When multiple platforms expose the same template, pass the 'platform' " +
			"argument (e.g. 'iOS', 'macOS') to disambiguate. For extensions that " +
			"must be hosted by an app, pass 'embed_in' with the host target name.",
	}, SafeTool("xcode_add_target", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		TemplateName string `json:"template_name" jsonschema:"Target template name (e.g. 'Widget Extension', 'App Intent Extension')"`
		ProductName  string `json:"product_name" jsonschema:"Product name for the new target"`
		BundleID     string `json:"bundle_id,omitempty" jsonschema:"Bundle identifier (auto-derived if not specified)"`
		Team         string `json:"team,omitempty" jsonschema:"Development team name to select"`
		Platform     string `json:"platform,omitempty" jsonschema:"Platform tab to select in the template chooser (e.g. 'iOS', 'macOS', 'watchOS', 'tvOS', 'visionOS', 'Multiplatform')"`
		EmbedIn      string `json:"embed_in,omitempty" jsonschema:"Host application target name for the 'Embed in Application' popup"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.TemplateName == "" {
			return errResult("template_name is required"), SimulatorActionOutput{}, nil
		}
		if args.ProductName == "" {
			return errResult("product_name is required"), SimulatorActionOutput{}, nil
		}

		app, err := axuiautomation.NewApplication(xcodewizard.XcodeBundleID)
		if err != nil {
			return errResult("Xcode is not running: " + err.Error()), SimulatorActionOutput{}, nil
		}
		defer app.Close()

		if err := xcodewizard.AddTarget(app, xcodewizard.Options{
			TemplateName: args.TemplateName,
			ProductName:  args.ProductName,
			BundleID:     args.BundleID,
			Team:         args.Team,
			Platform:     args.Platform,
			EmbedIn:      args.EmbedIn,
		}); err != nil {
			return errResult(err.Error()), SimulatorActionOutput{}, nil
		}

		msg := fmt.Sprintf("added target %q (template: %s)", args.ProductName, args.TemplateName)
		if verifyErr := verifyTargetScheme(ctx, req, args.ProductName); verifyErr != nil {
			msg += "; warning: " + verifyErr.Error()
		} else {
			msg += "; verified scheme present on disk"
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}, SimulatorActionOutput{Message: msg}, nil
	}))
}

// verifyTargetScheme re-reads the project on disk and confirms that a scheme
// with the given product name is listed. It returns nil when the scheme is
// present and a descriptive error otherwise.
func verifyTargetScheme(ctx context.Context, req *mcp.CallToolRequest, productName string) error {
	root := sessionProjectRoot(ctx, req.Session, ".")
	projects, err := project.Discover(root)
	if err != nil {
		return fmt.Errorf("discover projects under %s: %w", root, err)
	}
	if len(projects) == 0 {
		return fmt.Errorf("no xcodeproj/xcworkspace found under %s", root)
	}
	var errs []string
	for i := range projects {
		p := &projects[i]
		schemes, err := p.GetSchemes(ctx)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(p.Path), err))
			continue
		}
		for _, s := range schemes {
			if s == productName {
				return nil
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("scheme %q not found; errors: %v", productName, errs)
	}
	return fmt.Errorf("scheme %q not found in %s", productName, root)
}

func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
