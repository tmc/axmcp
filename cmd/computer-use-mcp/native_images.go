package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

func nativeImageContent(state *computeruse.AppState) (*mcp.ImageContent, error) {
	encoded := state.ScreenshotPNGBase64
	state.ScreenshotPNGBase64 = ""
	if encoded == "" && state.ScreenshotMetadata == nil {
		return nil, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot base64: %w", err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot PNG: %w", err)
	}
	info := state.ScreenshotMetadata
	if info == nil || info.ImageID != fmt.Sprintf("%x", sha256.Sum256(data)) || info.Width != cfg.Width || info.Height != cfg.Height {
		return nil, fmt.Errorf("screenshot image metadata mismatch")
	}
	return &mcp.ImageContent{Data: data, MIMEType: "image/png"}, nil
}

func nativeToolResult(out any, image *mcp.ImageContent) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	result := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
	if image != nil {
		result.Content = append(result.Content, image)
	}
	return result, nil
}

func nativeActResult(out nativeActOutput) (*mcp.CallToolResult, nativeActOutput, error) {
	var image *mcp.ImageContent
	if out.FreshState != nil {
		// Strip only the wire copy. The runner's stored observation retains its PNG.
		fresh := *out.FreshState
		out.FreshState = &fresh
		var err error
		image, err = nativeImageContent(&fresh.AppState)
		if err != nil {
			appendNativeError(&out, "encode post-action observation", err)
			out.FreshState = nil
			out.Observation = "unavailable"
		}
	}
	result, err := nativeToolResult(out, image)
	if err != nil {
		appendNativeError(&out, "encode post-action observation", err)
		out.FreshState = nil
		out.Observation = "unavailable"
		result, _ = nativeToolResult(out, nil)
	}
	// Encoding a post-action observation cannot erase the execution outcome.
	return result, out, nil
}
