package main

import "github.com/modelcontextprotocol/go-sdk/mcp"

func readOnlyToolAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		DestructiveHint: boolPtr(false),
		IdempotentHint:  true,
		OpenWorldHint:   boolPtr(false),
		ReadOnlyHint:    true,
	}
}

func actionToolAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		DestructiveHint: boolPtr(false),
		IdempotentHint:  false,
		OpenWorldHint:   boolPtr(false),
		ReadOnlyHint:    false,
	}
}

func boolPtr(v bool) *bool {
	return &v
}
