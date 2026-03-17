package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/internal/simctl"
)

// Video Recording Tools

type VideoStartInput struct {
	UDID       string `json:"udid,omitempty"`
	OutputPath string `json:"output_path,omitempty"`
	Codec      string `json:"codec,omitempty"`
}

type VideoStartOutput struct {
	RecordingID string `json:"recording_id"`
	FilePath    string `json:"file_path"`
	Message     string `json:"message"`
}

type VideoStopInput struct {
	RecordingID string `json:"recording_id"`
}

type VideoStopOutput struct {
	FilePath string `json:"file_path"`
	Message  string `json:"message"`
}

type VideoListOutput struct {
	Recordings []string `json:"recordings"`
}

func registerVideoTools(s *mcp.Server) {
	// Video Start Recording
	mcp.AddTool(s, &mcp.Tool{
		Name:        "video_start",
		Description: "Start recording video from a simulator. Returns a recording ID to use with video_stop. If udid is not specified, uses the first booted simulator.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args VideoStartInput) (*mcp.CallToolResult, VideoStartOutput, error) {
		udid := args.UDID
		if udid == "" {
			udid = "booted"
		}

		id, err := simctl.StartVideoRecording(ctx, udid, args.OutputPath, args.Codec)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to start recording: %v", err)},
				},
			}, VideoStartOutput{}, nil
		}

		// Get the file path from active recordings
		filePath := args.OutputPath
		if filePath == "" {
			filePath = fmt.Sprintf("/tmp/simrecord_%s.mp4", id)
		}

		return &mcp.CallToolResult{}, VideoStartOutput{
			RecordingID: id,
			FilePath:    filePath,
			Message:     fmt.Sprintf("Recording started with ID: %s", id),
		}, nil
	})

	// Video Stop Recording
	mcp.AddTool(s, &mcp.Tool{
		Name:        "video_stop",
		Description: "Stop an active video recording and return the output file path",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args VideoStopInput) (*mcp.CallToolResult, VideoStopOutput, error) {
		filePath, err := simctl.StopVideoRecording(args.RecordingID)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to stop recording: %v", err)},
				},
			}, VideoStopOutput{}, nil
		}

		return &mcp.CallToolResult{}, VideoStopOutput{
			FilePath: filePath,
			Message:  fmt.Sprintf("Recording stopped. Video saved to: %s", filePath),
		}, nil
	})

	// Video List Active Recordings
	mcp.AddTool(s, &mcp.Tool{
		Name:        "video_list",
		Description: "List all active video recordings",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, VideoListOutput, error) {
		ids := simctl.ListActiveRecordings()
		return &mcp.CallToolResult{}, VideoListOutput{Recordings: ids}, nil
	})
}
