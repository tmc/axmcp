package xcodebuild

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type BuildOptions struct {
	Project       string // Path to .xcodeproj
	Workspace     string // Path to .xcworkspace (mutually exclusive with Project)
	Scheme        string
	Configuration string // Debug, Release
	Destination   string // e.g., "platform=iOS Simulator,name=iPhone 15"
	DerivedData   string // Custom derived data path
}

type BuildProduct struct {
	Name             string `json:"name,omitempty"`
	Target           string `json:"target,omitempty"`
	Configuration    string `json:"configuration,omitempty"`
	Platform         string `json:"platform,omitempty"`
	SDK              string `json:"sdk,omitempty"`
	ProductType      string `json:"product_type,omitempty"`
	BundleID         string `json:"bundle_id,omitempty"`
	BundlePath       string `json:"bundle_path,omitempty"`
	ExecutablePath   string `json:"executable_path,omitempty"`
	BuiltProductsDir string `json:"built_products_dir,omitempty"`
}

type FileDiagnostics struct {
	File     string   `json:"file,omitempty"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type DiagnosticsSummary struct {
	ErrorCount   int               `json:"error_count"`
	WarningCount int               `json:"warning_count"`
	Files        []FileDiagnostics `json:"files,omitempty"`
}

type BuildProductsResult struct {
	Scheme         string         `json:"scheme,omitempty"`
	Configuration  string         `json:"configuration,omitempty"`
	Destination    string         `json:"destination,omitempty"`
	TargetPlatform string         `json:"target_platform,omitempty"`
	Products       []BuildProduct `json:"products,omitempty"`
}

type BuildResult struct {
	Action         string             `json:"action,omitempty"`
	Success        bool               `json:"success"`
	Duration       time.Duration      `json:"duration"`
	Output         string             `json:"output,omitempty"`
	Errors         []string           `json:"errors,omitempty"`
	Warnings       []string           `json:"warnings,omitempty"`
	Scheme         string             `json:"scheme,omitempty"`
	Configuration  string             `json:"configuration,omitempty"`
	Destination    string             `json:"destination,omitempty"`
	TargetPlatform string             `json:"target_platform,omitempty"`
	Products       []BuildProduct     `json:"products,omitempty"`
	Diagnostics    DiagnosticsSummary `json:"diagnostics"`
}

type buildSettingEntry struct {
	BuildSettings map[string]string `json:"buildSettings"`
	Action        string            `json:"action"`
	Target        string            `json:"target"`
}

var diagnosticLineRE = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?:\s+(warning|error):\s+(.*)$`)

// Build runs xcodebuild build.
func Build(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	return runXcodebuild(ctx, "build", opts)
}

// Test runs xcodebuild test.
func Test(ctx context.Context, opts BuildOptions) (*BuildResult, error) {
	return runXcodebuild(ctx, "test", opts)
}

// ShowBuildProducts resolves the build products for the selected scheme/configuration
// without requiring callers to scrape build logs.
func ShowBuildProducts(ctx context.Context, opts BuildOptions) (*BuildProductsResult, error) {
	entries, err := loadBuildSettings(ctx, opts)
	if err != nil {
		return nil, err
	}
	products := productsFromSettings(entries, opts)
	return &BuildProductsResult{
		Scheme:         opts.Scheme,
		Configuration:  effectiveConfiguration(opts, entries),
		Destination:    opts.Destination,
		TargetPlatform: targetPlatform(products, entries),
		Products:       products,
	}, nil
}

// PrimaryAppProduct returns the first .app product, if any.
func PrimaryAppProduct(products []BuildProduct) *BuildProduct {
	for i := range products {
		if strings.HasSuffix(products[i].BundlePath, ".app") {
			return &products[i]
		}
	}
	return nil
}

func runXcodebuild(ctx context.Context, action string, opts BuildOptions) (*BuildResult, error) {
	start := time.Now()

	var products []BuildProduct
	entries, settingsErr := loadBuildSettings(ctx, opts)
	if settingsErr == nil {
		products = productsFromSettings(entries, opts)
	}

	cmd := exec.CommandContext(ctx, "xcodebuild", xcodebuildArgs(action, opts)...)
	out, err := cmd.CombinedOutput()
	output := string(out)
	diagnostics := parseDiagnostics(output)

	result := &BuildResult{
		Action:         action,
		Success:        err == nil && !containsFailureMarker(output),
		Duration:       time.Since(start),
		Output:         output,
		Errors:         diagnosticsFlat(diagnostics, true),
		Warnings:       diagnosticsFlat(diagnostics, false),
		Scheme:         opts.Scheme,
		Configuration:  effectiveConfiguration(opts, entries),
		Destination:    opts.Destination,
		TargetPlatform: targetPlatform(products, entries),
		Products:       products,
		Diagnostics:    diagnostics,
	}

	if len(result.Errors) == 0 && !result.Success {
		if settingsErr != nil {
			result.Errors = []string{fmt.Sprintf("build failed and build settings were unavailable: %v", settingsErr)}
		} else {
			result.Errors = []string{"build failed with unknown error (check output)"}
		}
		result.Diagnostics.ErrorCount = len(result.Errors)
	}

	return result, nil
}

func xcodebuildArgs(action string, opts BuildOptions) []string {
	args := []string{action}
	if opts.Workspace != "" {
		args = append(args, "-workspace", opts.Workspace)
	} else if opts.Project != "" {
		args = append(args, "-project", opts.Project)
	}
	if opts.Scheme != "" {
		args = append(args, "-scheme", opts.Scheme)
	}
	if opts.Configuration != "" {
		args = append(args, "-configuration", opts.Configuration)
	}
	if opts.Destination != "" {
		args = append(args, "-destination", opts.Destination)
	}
	if opts.DerivedData != "" {
		args = append(args, "-derivedDataPath", opts.DerivedData)
	}
	return args
}

func showBuildSettingsArgs(opts BuildOptions) []string {
	args := []string{"-showBuildSettings", "-json"}
	if opts.Workspace != "" {
		args = append(args, "-workspace", opts.Workspace)
	} else if opts.Project != "" {
		args = append(args, "-project", opts.Project)
	}
	if opts.Scheme != "" {
		args = append(args, "-scheme", opts.Scheme)
	}
	if opts.Configuration != "" {
		args = append(args, "-configuration", opts.Configuration)
	}
	if opts.Destination != "" {
		args = append(args, "-destination", opts.Destination)
	}
	if opts.DerivedData != "" {
		args = append(args, "-derivedDataPath", opts.DerivedData)
	}
	return args
}

func loadBuildSettings(ctx context.Context, opts BuildOptions) ([]buildSettingEntry, error) {
	settingsCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		settingsCtx, cancel = context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(settingsCtx, "xcodebuild", showBuildSettingsArgs(opts)...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("xcodebuild -showBuildSettings failed: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("xcodebuild -showBuildSettings failed: %w", err)
	}

	var entries []buildSettingEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parse build settings: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no build settings returned")
	}
	return entries, nil
}

func productsFromSettings(entries []buildSettingEntry, opts BuildOptions) []BuildProduct {
	seen := make(map[string]bool)
	products := make([]BuildProduct, 0, len(entries))
	for _, entry := range entries {
		product, ok := productFromEntry(entry, opts)
		if !ok {
			continue
		}
		key := product.BundlePath + "\x00" + product.ExecutablePath + "\x00" + product.Target
		if seen[key] {
			continue
		}
		seen[key] = true
		products = append(products, product)
	}
	sort.SliceStable(products, func(i, j int) bool {
		ai := products[i]
		aj := products[j]
		switch {
		case strings.HasSuffix(ai.BundlePath, ".app") != strings.HasSuffix(aj.BundlePath, ".app"):
			return strings.HasSuffix(ai.BundlePath, ".app")
		case ai.Name != aj.Name:
			return ai.Name < aj.Name
		default:
			return ai.Target < aj.Target
		}
	})
	return products
}

func productFromEntry(entry buildSettingEntry, opts BuildOptions) (BuildProduct, bool) {
	settings := entry.BuildSettings
	targetBuildDir := settings["TARGET_BUILD_DIR"]
	builtProductsDir := firstNonEmpty(settings["BUILT_PRODUCTS_DIR"], targetBuildDir)
	fullProductName := firstNonEmpty(settings["FULL_PRODUCT_NAME"], settings["WRAPPER_NAME"])
	if targetBuildDir == "" && builtProductsDir == "" && fullProductName == "" {
		return BuildProduct{}, false
	}

	bundlePath := ""
	if wrapperName := settings["WRAPPER_NAME"]; wrapperName != "" {
		bundlePath = filepath.Join(firstNonEmpty(targetBuildDir, builtProductsDir), wrapperName)
	} else if strings.HasSuffix(fullProductName, ".app") {
		bundlePath = filepath.Join(firstNonEmpty(targetBuildDir, builtProductsDir), fullProductName)
	}

	executablePath := ""
	switch {
	case targetBuildDir != "" && settings["EXECUTABLE_PATH"] != "":
		executablePath = filepath.Join(targetBuildDir, settings["EXECUTABLE_PATH"])
	case bundlePath != "" && settings["EXECUTABLE_NAME"] != "":
		executablePath = filepath.Join(bundlePath, "Contents", "MacOS", settings["EXECUTABLE_NAME"])
	}

	name := firstNonEmpty(settings["PRODUCT_NAME"], settings["TARGET_NAME"], strings.TrimSuffix(fullProductName, filepath.Ext(fullProductName)))
	return BuildProduct{
		Name:             name,
		Target:           firstNonEmpty(entry.Target, settings["TARGET_NAME"]),
		Configuration:    firstNonEmpty(opts.Configuration, settings["CONFIGURATION"]),
		Platform:         firstNonEmpty(settings["PLATFORM_NAME"], firstPlatform(settings["SUPPORTED_PLATFORMS"])),
		SDK:              settings["SDK_NAME"],
		ProductType:      firstNonEmpty(settings["PRODUCT_TYPE"], settings["MACH_O_TYPE"]),
		BundleID:         settings["PRODUCT_BUNDLE_IDENTIFIER"],
		BundlePath:       bundlePath,
		ExecutablePath:   executablePath,
		BuiltProductsDir: builtProductsDir,
	}, true
}

func parseDiagnostics(output string) DiagnosticsSummary {
	type bucket struct {
		errors   []string
		warnings []string
	}

	files := map[string]*bucket{}
	summary := DiagnosticsSummary{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := diagnosticLineRE.FindStringSubmatch(line)
		if m == nil {
			switch {
			case strings.Contains(strings.ToLower(line), " error:"):
				summary.ErrorCount++
			case strings.Contains(strings.ToLower(line), " warning:"):
				summary.WarningCount++
			}
			continue
		}

		file := filepath.Clean(m[1])
		kind := m[4]
		msg := line
		group := files[file]
		if group == nil {
			group = &bucket{}
			files[file] = group
		}
		if kind == "error" {
			group.errors = append(group.errors, msg)
			summary.ErrorCount++
		} else {
			group.warnings = append(group.warnings, msg)
			summary.WarningCount++
		}
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		group := files[name]
		summary.Files = append(summary.Files, FileDiagnostics{
			File:     name,
			Errors:   append([]string(nil), group.errors...),
			Warnings: append([]string(nil), group.warnings...),
		})
	}
	return summary
}

func diagnosticsFlat(summary DiagnosticsSummary, errors bool) []string {
	var out []string
	for _, file := range summary.Files {
		if errors {
			out = append(out, file.Errors...)
			continue
		}
		out = append(out, file.Warnings...)
	}
	return out
}

func containsFailureMarker(output string) bool {
	return strings.Contains(output, "** BUILD FAILED **") || strings.Contains(output, "** TEST FAILED **")
}

func effectiveConfiguration(opts BuildOptions, entries []buildSettingEntry) string {
	if opts.Configuration != "" {
		return opts.Configuration
	}
	for _, entry := range entries {
		if cfg := entry.BuildSettings["CONFIGURATION"]; cfg != "" {
			return cfg
		}
	}
	return ""
}

func targetPlatform(products []BuildProduct, entries []buildSettingEntry) string {
	for _, product := range products {
		if product.Platform != "" {
			return product.Platform
		}
	}
	for _, entry := range entries {
		if platform := entry.BuildSettings["PLATFORM_NAME"]; platform != "" {
			return platform
		}
	}
	return ""
}

func firstPlatform(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
