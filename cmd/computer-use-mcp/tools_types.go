package main

import (
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listAppsInput struct{}

type getAppStateInput struct {
	App string `json:"app"`
}

type clickInput struct {
	App           string   `json:"app"`
	StateID       string   `json:"state_id"`
	ElementIndex  *string  `json:"element_index,omitempty"`
	X             *float64 `json:"x,omitempty"`
	Y             *float64 `json:"y,omitempty"`
	MouseButton   string   `json:"mouse_button,omitempty"`
	ClickCount    int      `json:"click_count,omitempty"`
	ForegroundHID bool     `json:"foreground_hid,omitempty"`
}

type performSecondaryActionInput struct {
	App          string `json:"app"`
	StateID      string `json:"state_id"`
	ElementIndex string `json:"element_index"`
	Action       string `json:"action"`
}

type scrollInput struct {
	App          string  `json:"app"`
	StateID      string  `json:"state_id"`
	ElementIndex string  `json:"element_index"`
	Direction    string  `json:"direction"`
	Pages        float64 `json:"pages,omitempty"`
}

type dragInput struct {
	App     string  `json:"app"`
	StateID string  `json:"state_id"`
	FromX   float64 `json:"from_x"`
	FromY   float64 `json:"from_y"`
	ToX     float64 `json:"to_x"`
	ToY     float64 `json:"to_y"`
}

type typeTextInput struct {
	App          string  `json:"app"`
	StateID      string  `json:"state_id"`
	ElementIndex *string `json:"element_index,omitempty"`
	Text         string  `json:"text"`
}

type pressKeyInput struct {
	App     string `json:"app"`
	StateID string `json:"state_id"`
	Key     string `json:"key"`
}

type setValueInput struct {
	App          string `json:"app"`
	StateID      string `json:"state_id"`
	ElementIndex string `json:"element_index"`
	Value        string `json:"value"`
}

type evaluateJavascriptInput struct {
	App         string `json:"app"`
	StateID     string `json:"state_id"`
	Script      string `json:"script"`
	WindowIndex int    `json:"window_index,omitempty"`
	TabIndex    int    `json:"tab_index,omitempty"`
}

type evaluateCDPJavascriptInput struct {
	App     string  `json:"app"`
	StateID string  `json:"state_id"`
	Script  string  `json:"script"`
	PID     int     `json:"pid,omitempty"`
	Port    int     `json:"port,omitempty"`
	Timeout float64 `json:"timeout,omitempty"`
}

type evaluateJavascriptOutput struct {
	SessionID string `json:"session_id,omitempty"`
	StateID   string `json:"state_id,omitempty"`
	Action    string `json:"action,omitempty"`
	Result    string `json:"result,omitempty"`
}

type evaluateCDPJavascriptOutput struct {
	SessionID   string `json:"session_id,omitempty"`
	StateID     string `json:"state_id,omitempty"`
	Action      string `json:"action,omitempty"`
	Type        string `json:"type,omitempty"`
	Value       any    `json:"value,omitempty"`
	Description string `json:"description,omitempty"`
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

func exactObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func numberProperty(description string) map[string]any {
	return map[string]any{
		"type":        "number",
		"description": description,
	}
}

func integerProperty(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
	}
}

func booleanProperty(description string) map[string]any {
	return map[string]any{
		"type":        "boolean",
		"description": description,
	}
}

func enumStringProperty(description string, values ...string) map[string]any {
	property := stringProperty(description)
	property["enum"] = values
	return property
}
