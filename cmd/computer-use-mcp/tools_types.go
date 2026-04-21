package main

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/computeruse"
)

type listAppsInput struct{}

type getAppStateInput struct {
	App             string `json:"app"`
	Window          string `json:"window,omitempty"`
	Approve         bool   `json:"approve,omitempty"`
	PersistApproval bool   `json:"persist_approval,omitempty"`
}

type clickInput struct {
	StateID      string `json:"state_id"`
	ElementIndex *int   `json:"element_index,omitempty"`
	X            *int   `json:"x,omitempty"`
	Y            *int   `json:"y,omitempty"`
	Button       string `json:"button,omitempty"`
	ClickCount   int    `json:"click_count,omitempty"`
}

type performSecondaryActionInput struct {
	StateID      string `json:"state_id"`
	ElementIndex int    `json:"element_index"`
	Action       string `json:"action"`
}

type scrollInput struct {
	StateID      string  `json:"state_id"`
	ElementIndex *int    `json:"element_index,omitempty"`
	Direction    string  `json:"direction"`
	Pages        float64 `json:"pages,omitempty"`
}

type dragInput struct {
	StateID string `json:"state_id"`
	StartX  int    `json:"start_x"`
	StartY  int    `json:"start_y"`
	EndX    int    `json:"end_x"`
	EndY    int    `json:"end_y"`
	Button  string `json:"button,omitempty"`
}

type typeTextInput struct {
	StateID      string `json:"state_id"`
	ElementIndex *int   `json:"element_index,omitempty"`
	Text         string `json:"text"`
}

type pressKeyInput struct {
	StateID string `json:"state_id"`
	Keys    string `json:"keys"`
}

type setValueInput struct {
	StateID      string `json:"state_id"`
	ElementIndex int    `json:"element_index"`
	Value        string `json:"value"`
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

func missingCoordinatesError() error {
	return fmt.Errorf("provide either element_index or both x and y")
}

func approvalRequiredResult(state computeruse.AppState) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: state.Approval.Message},
		},
	}
}
