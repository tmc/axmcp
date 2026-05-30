package macsigning

import (
	"os"

	"github.com/tmc/macgo"
	"github.com/tmc/macgo/codesign"
)

var findDeveloperID = codesign.FindDeveloperID

// Configure applies the default code-signing policy for local macgo apps.
//
// If the caller or environment already selected a signing mode, Configure
// leaves it alone. Otherwise it prefers a Developer ID Application certificate,
// which gives TCC a stable code requirement, and falls back to ad-hoc signing
// when no such identity is available.
func Configure(cfg *macgo.Config) *macgo.Config {
	if cfg == nil {
		return nil
	}
	if cfg.BundleID != "" && cfg.CodeSigningIdentifier == "" {
		cfg.CodeSigningIdentifier = cfg.BundleID
	}
	applySigningEnv(cfg)
	if cfg.CodeSignIdentity != "" || cfg.AutoSign || cfg.AdHocSign {
		return cfg
	}
	if identity := findDeveloperID(); identity != "" {
		return cfg.WithCodeSigning(identity)
	}
	return cfg.WithAdHocSign()
}

func applySigningEnv(cfg *macgo.Config) {
	if cfg.CodeSignIdentity == "" {
		cfg.CodeSignIdentity = os.Getenv("MACGO_CODE_SIGN_IDENTITY")
	}
	if os.Getenv("MACGO_AUTO_SIGN") == "1" {
		cfg.AutoSign = true
	}
	if os.Getenv("MACGO_AD_HOC_SIGN") == "1" {
		cfg.AdHocSign = true
	}
}
