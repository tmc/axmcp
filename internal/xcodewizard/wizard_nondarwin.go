//go:build !darwin

// Package xcodewizard drives Xcode's target wizard on Darwin.
package xcodewizard

import "fmt"

// XcodeBundleID is the bundle identifier of the Xcode application.
const XcodeBundleID = "com.apple.dt.Xcode"

// Options describes a new Xcode target to add via File > New > Target.
type Options struct {
	TemplateName string
	ProductName  string
	BundleID     string
	Team         string
	Platform     string
	EmbedIn      string
}

// AddTarget reports that Xcode wizard automation is unavailable.
func AddTarget(_ any, opts Options) error {
	if opts.TemplateName == "" {
		return fmt.Errorf("template name is required")
	}
	if opts.ProductName == "" {
		return fmt.Errorf("product name is required")
	}
	return fmt.Errorf("xcode target wizard is not available on this platform")
}
