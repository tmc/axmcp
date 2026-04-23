package macsigning

import (
	"testing"

	"github.com/tmc/macgo"
)

func TestConfigureSetsIdentifier(t *testing.T) {
	cfg := macgo.NewConfig()
	cfg.BundleID = "dev.tmc.axmcp"

	originalFindDeveloperID := findDeveloperID
	findDeveloperID = func() string { return "" }
	defer func() { findDeveloperID = originalFindDeveloperID }()

	Configure(cfg)
	if got := cfg.CodeSigningIdentifier; got != cfg.BundleID {
		t.Fatalf("CodeSigningIdentifier = %q, want %q", got, cfg.BundleID)
	}
	if !cfg.AdHocSign {
		t.Fatal("AdHocSign = false, want true when no certificate is available")
	}
}

func TestConfigurePrefersDeveloperID(t *testing.T) {
	cfg := macgo.NewConfig()
	cfg.BundleID = "dev.tmc.axmcp"

	originalFindDeveloperID := findDeveloperID
	findDeveloperID = func() string { return "Developer ID Application: Example (TEAMID1234)" }
	defer func() { findDeveloperID = originalFindDeveloperID }()

	Configure(cfg)
	if got := cfg.CodeSignIdentity; got != "Developer ID Application: Example (TEAMID1234)" {
		t.Fatalf("CodeSignIdentity = %q, want stable identity", got)
	}
	if cfg.AdHocSign {
		t.Fatal("AdHocSign = true, want false when a Developer ID identity is available")
	}
}

func TestConfigureRespectsExistingMode(t *testing.T) {
	cfg := macgo.NewConfig().WithAdHocSign()
	cfg.BundleID = "dev.tmc.axmcp"

	originalFindDeveloperID := findDeveloperID
	findDeveloperID = func() string { return "Developer ID Application: Example (TEAMID1234)" }
	defer func() { findDeveloperID = originalFindDeveloperID }()

	Configure(cfg)
	if got := cfg.CodeSignIdentity; got != "" {
		t.Fatalf("CodeSignIdentity = %q, want empty when caller already selected ad-hoc signing", got)
	}
	if !cfg.AdHocSign {
		t.Fatal("AdHocSign = false, want caller-selected mode preserved")
	}
}
