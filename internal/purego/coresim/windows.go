//go:build windows

package coresim

import (
	"fmt"

	"github.com/tmc/axmcp/internal/purego/objc"
)

const fallbackBridgeDelegateToken = "coresim-fallback-token"

// AccessibilityElement represents an iOS accessibility element.
type AccessibilityElement struct {
	AXLabel      string                  `json:"AXLabel,omitempty"`
	AXValue      string                  `json:"AXValue,omitempty"`
	AXIdentifier string                  `json:"AXIdentifier,omitempty"`
	AXFrame      map[string]float64      `json:"AXFrame,omitempty"`
	AXUniqueId   string                  `json:"AXUniqueId,omitempty"`
	AXTraits     uint64                  `json:"AXTraits,omitempty"`
	AXEnabled    bool                    `json:"AXEnabled,omitempty"`
	AXChildren   []*AccessibilityElement `json:"AXChildren,omitempty"`
	Role         string                  `json:"role,omitempty"`
	PID          int32                   `json:"pid,omitempty"`
	RawData      []byte                  `json:"-"`
	RawElement   map[string]interface{}  `json:"-"`
	Token        string                  `json:"-"`
}

type SimDeviceLegacyClient struct {
	id objc.ID
}

const (
	AXPRequestTypeElement   = 0
	AXPRequestTypeAttribute = 1
	AXPRequestTypeAction    = 2
	AXPRequestTypeHitTest   = 4

	AXPAttributeTypeChildren = 5
)

func unsupported() error {
	return fmt.Errorf("coresimulator is not available on windows")
}

func CreateQueue(string) uintptr {
	return 0
}

func InitAXPDelegate() {}

func RegisterDeviceForToken(*SimDevice) string {
	return fallbackBridgeDelegateToken
}

func UnregisterToken(string) {}

func (d SimDevice) SendAccessibilityRequestID(objc.ID) (objc.ID, error) {
	if d.id == 0 {
		return 0, fmt.Errorf("device is nil")
	}
	return 0, unsupported()
}

func (d SimDevice) GetFrontmostApplicationElement(string) (*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (d SimDevice) GetAccessibilityElements() ([]*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (d SimDevice) GetAccessibilityElementsForPID(int) ([]*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (d SimDevice) FetchChildren(*AccessibilityElement) ([]*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (d SimDevice) RecursivelyFetchChildren(*AccessibilityElement, int) {}

func (d SimDevice) UpgradeElement(*AccessibilityElement) (*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (d SimDevice) GetAccessibilityElementAtPoint(float64, float64, string) (*AccessibilityElement, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (d SimDevice) PerformAction(*AccessibilityElement, uint64) error {
	if d.id == 0 {
		return fmt.Errorf("device is nil")
	}
	return unsupported()
}

func (d SimDevice) ConnectToHID() (*SimDeviceLegacyClient, error) {
	if d.id == 0 {
		return nil, fmt.Errorf("device is nil")
	}
	return nil, unsupported()
}

func (c *SimDeviceLegacyClient) SendMessage(msg []byte) error {
	if c == nil || c.id == 0 {
		return fmt.Errorf("hid client is nil")
	}
	if len(msg) == 0 {
		return fmt.Errorf("hid message is empty")
	}
	return unsupported()
}

func (d SimDevice) Tap(float64, float64) error {
	if d.id == 0 {
		return fmt.Errorf("device is nil")
	}
	return unsupported()
}
