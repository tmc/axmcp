package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	previewutil "github.com/tmc/axmcp/internal/preview"
)

const swiftUIPreviewPromptName = "swiftui_preview_block"

func registerSwiftUIPreviewFeatures(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        swiftUIPreviewPromptName,
		Title:       "SwiftUI Preview Block",
		Description: "Generate a SwiftUI #Preview block for a view source file",
		Arguments: []*mcp.PromptArgument{
			{
				Name:        "path",
				Title:       "Source Path",
				Description: "Path to the Swift source file",
				Required:    true,
			},
			{
				Name:        "type_name",
				Title:       "View Type",
				Description: "Optional view type name if the file defines multiple views",
			},
			{
				Name:        "notes",
				Title:       "Notes",
				Description: "Additional preview requirements",
			},
			{
				Name:        "variants",
				Title:       "Variants",
				Description: "Comma-separated preview variants to cover",
			},
		},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		args := req.Params.Arguments
		spec, err := previewutil.Prepare(args["path"], args["type_name"], previewutil.ParseVariants(args["variants"]), args["notes"])
		if err != nil {
			return nil, err
		}
		instructions := spec.Instructions
		if rules := mustReadSkill("preview-rules.md"); rules != "" {
			instructions += "\n\n" + rules
		}
		return &mcp.GetPromptResult{
			Description: "Generate a SwiftUI preview block from embedded source context",
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: instructions},
				},
				{
					Role: "user",
					Content: &mcp.EmbeddedResource{
						Resource: &mcp.ResourceContents{
							URI:      spec.SourceURI,
							MIMEType: spec.SourceMIME,
							Text:     spec.Source,
						},
					},
				},
			},
		}, nil
	})
}
