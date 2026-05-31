//go:build darwin

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerSwiftPMPreviewRestructure(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "swiftpm_preview_restructure",
		Title:       "SwiftPM Preview Restructure",
		Description: "Restructure a SwiftPM package to move SwiftUI views from an executable target into a library target, enabling Xcode previews without ENABLE_DEBUG_DYLIB",
		Arguments: []*mcp.PromptArgument{
			{
				Name:        "package_path",
				Title:       "Package Path",
				Description: "Path to the SwiftPM package directory containing Package.swift",
				Required:    true,
			},
			{
				Name:        "executable_target",
				Title:       "Executable Target",
				Description: "Name of the executable target containing SwiftUI views to extract",
			},
			{
				Name:        "library_name",
				Title:       "Library Name",
				Description: "Name for the new UI library target (e.g. MyAppUI)",
			},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		packagePath := args["package_path"]
		execTarget := args["executable_target"]
		libName := args["library_name"]

		instructions := mustReadSkill("swiftpm-preview-restructure.md")
		if packagePath != "" {
			instructions += "\n\n## Context\n\nPackage path: " + packagePath
		}
		if execTarget != "" {
			instructions += "\nExecutable target to restructure: " + execTarget
		}
		if libName != "" {
			instructions += "\nNew library target name: " + libName
		}

		return &mcp.GetPromptResult{
			Description: "Restructure SwiftPM package for SwiftUI preview support",
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: instructions},
				},
			},
		}, nil
	})
}
