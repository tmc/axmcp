package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/altool"
	"github.com/tmc/axmcp/internal/asc"
)

func registerASCTools(s *mcp.Server) {
	// asc_auth_status
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_auth_status",
		Description: "Check App Store Connect authentication status and key permissions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]any, error) {
		info, err := asc.AuthStatus(ctx)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{
			"authenticated": info.Authenticated,
			"output":        info.Output,
		}, nil
	})

	// asc_list_apps
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_list_apps",
		Description: "List apps in the App Store Connect account.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, map[string]any, error) {
		apps, err := asc.ListApps(ctx)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{
			"count": len(apps),
			"apps":  apps,
		}, nil
	})

	// asc_list_builds
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_list_builds",
		Description: "List builds for an app in App Store Connect.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		AppID string `json:"app_id" description:"App ID to list builds for"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		builds, err := asc.ListBuilds(ctx, args.AppID)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{
			"count":  len(builds),
			"builds": builds,
		}, nil
	})

	// asc_list_beta_groups
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_list_beta_groups",
		Description: "List TestFlight beta groups for an app.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		AppID string `json:"app_id,omitempty" description:"App ID to filter beta groups"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		groups, err := asc.ListBetaGroups(ctx, args.AppID)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{
			"count":  len(groups),
			"groups": groups,
		}, nil
	})

	// asc_create_beta_group
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_create_beta_group",
		Description: "Create a TestFlight beta group for an app.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		AppID string `json:"app_id" description:"App ID to create the group for"`
		Name  string `json:"name" description:"Name for the beta group"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		group, err := asc.CreateBetaGroup(ctx, args.AppID, args.Name)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		data, _ := json.Marshal(group)
		var result map[string]any
		_ = json.Unmarshal(data, &result)
		return &mcp.CallToolResult{}, result, nil
	})

	// asc_add_tester
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_add_tester",
		Description: "Add a beta tester by email to TestFlight group(s).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Email     string   `json:"email" description:"Tester email address"`
		FirstName string   `json:"first_name,omitempty" description:"Tester first name"`
		LastName  string   `json:"last_name,omitempty" description:"Tester last name"`
		GroupIDs  []string `json:"group_ids,omitempty" description:"Beta group IDs to add tester to"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		if err := asc.AddTester(ctx, args.Email, args.FirstName, args.LastName, args.GroupIDs); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{
			"message": fmt.Sprintf("Added tester %s", args.Email),
		}, nil
	})

	// asc_invite_user
	mcp.AddTool(s, &mcp.Tool{
		Name:        "asc_invite_user",
		Description: "Invite a user to App Store Connect with specified roles.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		Email     string   `json:"email" description:"User email address"`
		FirstName string   `json:"first_name" description:"User first name"`
		LastName  string   `json:"last_name" description:"User last name"`
		Roles     []string `json:"roles" description:"Roles: ADMIN, FINANCE, SALES, MARKETING, APP_MANAGER, DEVELOPER, etc."`
	}) (*mcp.CallToolResult, map[string]any, error) {
		if err := asc.InviteUser(ctx, args.Email, args.FirstName, args.LastName, args.Roles); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{
			"message": fmt.Sprintf("Invited user %s", args.Email),
		}, nil
	})

	// altool_upload_app
	mcp.AddTool(s, &mcp.Tool{
		Name:        "altool_upload_app",
		Description: "Upload an .ipa or .pkg to App Store Connect / TestFlight via altool.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		FilePath  string `json:"file_path" description:"Path to .ipa or .pkg file"`
		APIKey    string `json:"api_key" description:"App Store Connect API Key ID"`
		APIIssuer string `json:"api_issuer" description:"App Store Connect API Issuer ID"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := altool.UploadApp(ctx, args.FilePath, args.APIKey, args.APIIssuer)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{"output": out}, nil
	})

	// altool_validate_app
	mcp.AddTool(s, &mcp.Tool{
		Name:        "altool_validate_app",
		Description: "Validate an .ipa or .pkg archive before uploading.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		FilePath  string `json:"file_path" description:"Path to .ipa or .pkg file"`
		APIKey    string `json:"api_key" description:"App Store Connect API Key ID"`
		APIIssuer string `json:"api_issuer" description:"App Store Connect API Issuer ID"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := altool.ValidateApp(ctx, args.FilePath, args.APIKey, args.APIIssuer)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{"output": out}, nil
	})

	// altool_list_apps
	mcp.AddTool(s, &mcp.Tool{
		Name:        "altool_list_apps",
		Description: "List apps for a provider via altool.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		ProviderID string `json:"provider_id,omitempty" description:"ASC provider ID"`
		APIKey     string `json:"api_key" description:"App Store Connect API Key ID"`
		APIIssuer  string `json:"api_issuer" description:"App Store Connect API Issuer ID"`
	}) (*mcp.CallToolResult, map[string]any, error) {
		out, err := altool.ListApps(ctx, args.ProviderID, args.APIKey, args.APIIssuer)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil, nil
		}
		return &mcp.CallToolResult{}, map[string]any{"output": out}, nil
	})
}
