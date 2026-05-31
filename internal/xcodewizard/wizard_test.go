//go:build darwin

package xcodewizard

import (
	"strings"
	"testing"
)

func TestAddTargetValidatesRequiredOptions(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"missing template", Options{ProductName: "X"}, "template name is required"},
		{"missing product", Options{TemplateName: "Widget Extension"}, "product name is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AddTarget(nil, tc.opts)
			if err == nil {
				t.Fatalf("AddTarget(%+v) = nil, want error containing %q", tc.opts, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("AddTarget error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestXcodeBundleID(t *testing.T) {
	if XcodeBundleID != "com.apple.dt.Xcode" {
		t.Fatalf("unexpected XcodeBundleID %q", XcodeBundleID)
	}
}
