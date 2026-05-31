//go:build darwin

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// File System Tools

// Inputs
type FSPathInput struct {
	Path string `json:"path" description:"Absolute path to the file or directory"`
}

type FSMoveCopyInput struct {
	Source      string `json:"source" description:"Source path"`
	Destination string `json:"destination" description:"Destination path"`
}

type FSWriteInput struct {
	Path    string `json:"path" description:"Absolute path to the file"`
	Content string `json:"content" description:"Content to write to the file"`
}

// Outputs
type FSListOutput struct {
	Files []FileInfo `json:"files"`
}

type FileInfo struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
}

type FSReadOutput struct {
	Content string `json:"content"`
}

func registerFileSystemTools(s *mcp.Server) {

	// List Directory
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_list",
		Description: "List contents of a directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSPathInput) (*mcp.CallToolResult, FSListOutput, error) {
		entries, err := os.ReadDir(args.Path)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to list directory: %v", err)}}}, FSListOutput{}, nil
		}

		var files []FileInfo
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, FileInfo{
				Name:  entry.Name(),
				IsDir: entry.IsDir(),
				Size:  info.Size(),
				Mode:  info.Mode().String(),
			})
		}
		return &mcp.CallToolResult{}, FSListOutput{Files: files}, nil
	})

	// Read File
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_read",
		Description: "Read the contents of a file",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSPathInput) (*mcp.CallToolResult, FSReadOutput, error) {
		content, err := os.ReadFile(args.Path)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to read file: %v", err)}}}, FSReadOutput{}, nil
		}
		return &mcp.CallToolResult{}, FSReadOutput{Content: string(content)}, nil
	})

	// Write File
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_write",
		Description: "Write content to a file",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSWriteInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to write file: %v", err)}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Successfully wrote to %s", args.Path)}, nil
	})

	// Copy File/Dir
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_copy",
		Description: "Copy a file or directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSMoveCopyInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		// Simplified copy for single file example; a full implementation would use filepath.Walk for dirs
		sourceFile, err := os.Open(args.Source)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to open source: %v", err)}}}, SimulatorActionOutput{}, nil
		}
		defer sourceFile.Close()

		destFile, err := os.Create(args.Destination)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to create destination: %v", err)}}}, SimulatorActionOutput{}, nil
		}
		defer destFile.Close()

		_, err = io.Copy(destFile, sourceFile)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to copy content: %v", err)}}}, SimulatorActionOutput{}, nil
		}

		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Successfully copied %s to %s", args.Source, args.Destination)}, nil
	})

	// Move File/Dir
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_move",
		Description: "Move a file or directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSMoveCopyInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := os.Rename(args.Source, args.Destination); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to move file: %v", err)}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Successfully moved %s to %s", args.Source, args.Destination)}, nil
	})

	// Delete File/Dir
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_delete",
		Description: "Delete a file or directory",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSPathInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := os.RemoveAll(args.Path); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to delete path: %v", err)}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Successfully deleted %s", args.Path)}, nil
	})

	// Create Directory
	mcp.AddTool(s, &mcp.Tool{
		Name:        "fs_mkdir",
		Description: "Create a directory (recursively)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args FSPathInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if err := os.MkdirAll(args.Path, 0755); err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to create directory: %v", err)}}}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: fmt.Sprintf("Successfully created directory %s", args.Path)}, nil
	})
}
