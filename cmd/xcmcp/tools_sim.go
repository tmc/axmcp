package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/simctl"
)

// List Simulators
type ListSimulatorsInput struct {
	// No args
}

type ListSimulatorsOutput struct {
	Simulators []simctl.Simulator `json:"simulators"`
}

func registerListSimulators(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_simulators",
		Description: "List available iOS simulators",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args ListSimulatorsInput) (*mcp.CallToolResult, ListSimulatorsOutput, error) {
		sims, err := simctl.List(ctx)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to list simulators: %v", err)},
				},
			}, ListSimulatorsOutput{}, nil
		}
		return &mcp.CallToolResult{}, ListSimulatorsOutput{Simulators: sims}, nil
	})
}

// Boot Simulator
type SimulatorUDIDInput struct {
	UDID string `json:"udid"`
}

func registerBootSimulator(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "boot_simulator",
		Description: "Boot a simulator by UDID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SimulatorUDIDInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		udid := args.UDID
		if err := simctl.Boot(ctx, udid); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to boot simulator: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Simulator booted"}, nil
	})
}

// Shutdown Simulator
func registerShutdownSimulator(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "shutdown_simulator",
		Description: "Shutdown a simulator by UDID",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args SimulatorUDIDInput) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		udid := args.UDID
		if err := simctl.Shutdown(ctx, udid); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{
					&mcp.TextContent{Text: fmt.Sprintf("Failed to shutdown simulator: %v", err)},
				},
			}, SimulatorActionOutput{}, nil
		}
		return &mcp.CallToolResult{}, SimulatorActionOutput{Message: "Simulator shutdown"}, nil
	})
}
