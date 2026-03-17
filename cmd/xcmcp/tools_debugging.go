package main

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/internal/debugger"
)

var debugManager = debugger.NewManager()

type DebugAttachInput struct {
	BundleID       string  `json:"bundle_id,omitempty"`
	Name           string  `json:"name,omitempty"`
	PID            int     `json:"pid,omitempty"`
	TimeoutSeconds float64 `json:"timeout_seconds,omitempty"`
}

type DebugAttachOutput struct {
	Session *debugger.SessionInfo `json:"session,omitempty"`
	Output  string                `json:"output,omitempty"`
}

type DebugSessionsOutput struct {
	Sessions []debugger.SessionInfo `json:"sessions"`
}

type DebugCommandInput struct {
	SessionID      string  `json:"session_id"`
	Command        string  `json:"command"`
	TimeoutSeconds float64 `json:"timeout_seconds,omitempty"`
}

type DebugSessionIDInput struct {
	SessionID      string  `json:"session_id"`
	TimeoutSeconds float64 `json:"timeout_seconds,omitempty"`
}

type DebugBreakpointAddInput struct {
	SessionID      string  `json:"session_id"`
	File           string  `json:"file,omitempty"`
	Line           int     `json:"line,omitempty"`
	Name           string  `json:"name,omitempty"`
	Raw            string  `json:"raw,omitempty"`
	Address        string  `json:"address,omitempty"`
	TimeoutSeconds float64 `json:"timeout_seconds,omitempty"`
}

type DebugBreakpointRemoveInput struct {
	SessionID      string  `json:"session_id"`
	BreakpointID   int     `json:"breakpoint_id"`
	TimeoutSeconds float64 `json:"timeout_seconds,omitempty"`
}

type DebugCommandOutput struct {
	Output string `json:"output,omitempty"`
}

func registerDebuggingTools(s *mcp.Server) {
	registerDebugAttach(s)
	registerDebugListSessions(s)
	registerDebugCommand(s)
	registerDebugContinue(s)
	registerDebugStack(s)
	registerDebugVariables(s)
	registerDebugBreakpointAdd(s)
	registerDebugBreakpointRemove(s)
	registerDebugDetach(s)
}

func registerDebugAttach(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_attach",
		Description: "Attach LLDB to a running macOS app process identified by bundle id, app name, or pid.",
	}, SafeTool("debug_attach", func(ctx context.Context, req *mcp.CallToolRequest, args DebugAttachInput) (*mcp.CallToolResult, DebugAttachOutput, error) {
		attachCtx, cancel := context.WithTimeout(context.Background(), secondsWithDefault(args.TimeoutSeconds, 10*time.Second))
		defer cancel()

		session, output, err := debugManager.Attach(attachCtx, debugger.Selector{
			BundleID: args.BundleID,
			Name:     args.Name,
			PID:      args.PID,
		})
		if err != nil {
			return errResult("debug attach failed: " + err.Error()), DebugAttachOutput{}, nil
		}
		return &mcp.CallToolResult{}, DebugAttachOutput{
			Session: &session,
			Output:  output,
		}, nil
	}))
}

func registerDebugListSessions(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_list_sessions",
		Description: "List active LLDB debug sessions started by xcmcp.",
	}, SafeTool("debug_list_sessions", func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, DebugSessionsOutput, error) {
		return &mcp.CallToolResult{}, DebugSessionsOutput{Sessions: debugManager.List()}, nil
	}))
}

func registerDebugCommand(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_command",
		Description: "Run a raw LLDB command inside an existing debug session.",
	}, SafeTool("debug_command", func(ctx context.Context, req *mcp.CallToolRequest, args DebugCommandInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return runDebugCommand(args.SessionID, args.Command, args.TimeoutSeconds)
	}))
}

func registerDebugContinue(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_continue",
		Description: "Resume the attached process in an existing LLDB debug session.",
	}, SafeTool("debug_continue", func(ctx context.Context, req *mcp.CallToolRequest, args DebugSessionIDInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return withDebugTimeout(args.TimeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
			output, err := debugManager.Continue(runCtx, args.SessionID)
			if err != nil {
				return errResult("debug continue failed: " + err.Error()), DebugCommandOutput{}, nil
			}
			return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
		})
	}))
}

func registerDebugStack(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_stack",
		Description: "Show the current thread backtrace for an existing LLDB debug session.",
	}, SafeTool("debug_stack", func(ctx context.Context, req *mcp.CallToolRequest, args DebugSessionIDInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return withDebugTimeout(args.TimeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
			output, err := debugManager.Stack(runCtx, args.SessionID)
			if err != nil {
				return errResult("debug stack failed: " + err.Error()), DebugCommandOutput{}, nil
			}
			return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
		})
	}))
}

func registerDebugVariables(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_variables",
		Description: "Show frame variables for an existing LLDB debug session.",
	}, SafeTool("debug_variables", func(ctx context.Context, req *mcp.CallToolRequest, args DebugSessionIDInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return withDebugTimeout(args.TimeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
			output, err := debugManager.Variables(runCtx, args.SessionID)
			if err != nil {
				return errResult("debug variables failed: " + err.Error()), DebugCommandOutput{}, nil
			}
			return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
		})
	}))
}

func registerDebugBreakpointAdd(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_breakpoint_add",
		Description: "Set a breakpoint by symbol, file/line, address, or raw breakpoint arguments in an existing LLDB debug session.",
	}, SafeTool("debug_breakpoint_add", func(ctx context.Context, req *mcp.CallToolRequest, args DebugBreakpointAddInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return withDebugTimeout(args.TimeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
			output, err := debugManager.AddBreakpoint(runCtx, args.SessionID, debugger.BreakpointSpec{
				File:    args.File,
				Line:    args.Line,
				Name:    args.Name,
				Raw:     args.Raw,
				Address: args.Address,
			})
			if err != nil {
				return errResult("debug breakpoint add failed: " + err.Error()), DebugCommandOutput{}, nil
			}
			return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
		})
	}))
}

func registerDebugBreakpointRemove(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_breakpoint_remove",
		Description: "Remove a breakpoint by LLDB breakpoint id from an existing debug session.",
	}, SafeTool("debug_breakpoint_remove", func(ctx context.Context, req *mcp.CallToolRequest, args DebugBreakpointRemoveInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return withDebugTimeout(args.TimeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
			output, err := debugManager.RemoveBreakpoint(runCtx, args.SessionID, args.BreakpointID)
			if err != nil {
				return errResult("debug breakpoint remove failed: " + err.Error()), DebugCommandOutput{}, nil
			}
			return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
		})
	}))
}

func registerDebugDetach(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "debug_detach",
		Description: "Detach LLDB from an existing debug session and forget the session.",
	}, SafeTool("debug_detach", func(ctx context.Context, req *mcp.CallToolRequest, args DebugSessionIDInput) (*mcp.CallToolResult, DebugCommandOutput, error) {
		return withDebugTimeout(args.TimeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
			output, err := debugManager.Detach(runCtx, args.SessionID)
			if err != nil {
				return errResult("debug detach failed: " + err.Error()), DebugCommandOutput{}, nil
			}
			return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
		})
	}))
}

func runDebugCommand(sessionID, command string, timeoutSeconds float64) (*mcp.CallToolResult, DebugCommandOutput, error) {
	if command == "" {
		return errResult("command is required"), DebugCommandOutput{}, nil
	}
	return withDebugTimeout(timeoutSeconds, func(runCtx context.Context) (*mcp.CallToolResult, DebugCommandOutput, error) {
		output, err := debugManager.Run(runCtx, sessionID, command)
		if err != nil {
			return errResult("debug command failed: " + err.Error()), DebugCommandOutput{}, nil
		}
		return &mcp.CallToolResult{}, DebugCommandOutput{Output: output}, nil
	})
}

func withDebugTimeout(timeoutSeconds float64, fn func(context.Context) (*mcp.CallToolResult, DebugCommandOutput, error)) (*mcp.CallToolResult, DebugCommandOutput, error) {
	runCtx, cancel := context.WithTimeout(context.Background(), secondsWithDefault(timeoutSeconds, 5*time.Second))
	defer cancel()
	return fn(runCtx)
}
