//go:build darwin

package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerConventionPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "swift_conventions",
		Title:       "Swift Coding Conventions",
		Description: "Platform-aware Swift conventions: concurrency, testing, framework preferences, and Xcode 26 APIs",
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Description: "Swift coding conventions for Apple platform development",
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: mustReadSkill("swift-conventions.md")},
				},
			},
		}, nil
	})
}
