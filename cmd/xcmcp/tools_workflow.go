package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/macosapp"
	"github.com/tmc/axmcp/internal/xcodebuild"
)

type ShowBuildProductsOutput struct {
	Result *xcodebuild.BuildProductsResult `json:"result,omitempty"`
}

type RunBuiltAppInput struct {
	Project            string  `json:"project,omitempty"`
	Workspace          string  `json:"workspace,omitempty"`
	Scheme             string  `json:"scheme"`
	Configuration      string  `json:"configuration,omitempty"`
	Destination        string  `json:"destination,omitempty"`
	BuildIfNeeded      *bool   `json:"build_if_needed,omitempty"`
	Frontmost          *bool   `json:"frontmost,omitempty"`
	WaitForReady       *bool   `json:"wait_for_ready,omitempty"`
	WaitForWindow      *bool   `json:"wait_for_window,omitempty"`
	WaitForWindowTitle *bool   `json:"wait_for_window_title,omitempty"`
	WaitForContent     *bool   `json:"wait_for_content,omitempty"`
	TimeoutSeconds     float64 `json:"timeout_seconds,omitempty"`
}

type RunBuiltAppOutput struct {
	Build   *xcodebuild.BuildResult  `json:"build,omitempty"`
	Product *xcodebuild.BuildProduct `json:"product,omitempty"`
	App     *macosapp.ReadyState     `json:"app,omitempty"`
}

type WaitForAppReadyInput struct {
	BundleID           string  `json:"bundle_id,omitempty"`
	Name               string  `json:"name,omitempty"`
	PID                int     `json:"pid,omitempty"`
	TimeoutSeconds     float64 `json:"timeout_seconds,omitempty"`
	WaitForWindow      *bool   `json:"wait_for_window,omitempty"`
	WaitForWindowTitle *bool   `json:"wait_for_window_title,omitempty"`
	WaitForContent     *bool   `json:"wait_for_content,omitempty"`
}

type WaitForAppReadyOutput struct {
	App *macosapp.ReadyState `json:"app,omitempty"`
}

type RenderAllPreviewsInput struct {
	TabIdentifier string   `json:"tab_identifier,omitempty"`
	Root          string   `json:"root,omitempty"`
	Glob          string   `json:"glob,omitempty"`
	Files         []string `json:"files,omitempty"`
	Timeout       int      `json:"timeout,omitempty"`
}

type PreviewRenderResult struct {
	SourceFile   string `json:"source_file"`
	PreviewIndex int    `json:"preview_index"`
	Success      bool   `json:"success"`
	SnapshotPath string `json:"snapshot_path,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Error        string `json:"error,omitempty"`
}

type RenderAllPreviewsOutput struct {
	TabIdentifier string                `json:"tab_identifier,omitempty"`
	Results       []PreviewRenderResult `json:"results"`
}

var previewTokenRE = regexp.MustCompile(`(?m)#Preview\b|:\s*PreviewProvider\b`)

func registerNativeWorkflowTools(s *mcp.Server) {
	registerShowBuildProducts(s)
	registerRunBuiltApp(s)
	registerWaitForAppReady(s)
}

func registerXcodeWorkflowTools(s *mcp.Server) {
	registerRenderAllPreviews(s)
}

func registerShowBuildProducts(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "show_build_products",
		Title:       "Show Build Products",
		Description: "Show active build products for a scheme/configuration without scraping raw build logs.",
		Annotations: readOnlyTool("Show Build Products"),
	}, SafeTool("show_build_products", func(ctx context.Context, req *mcp.CallToolRequest, args BuildInput) (*mcp.CallToolResult, ShowBuildProductsOutput, error) {
		projectPath, workspacePath, err := inferBuildLocator(ctx, req.Session, args.Project, args.Workspace)
		if err != nil {
			return errResult("failed to infer project or workspace: " + err.Error()), ShowBuildProductsOutput{}, nil
		}
		result, err := xcodebuild.ShowBuildProducts(ctx, xcodebuild.BuildOptions{
			Project:       projectPath,
			Workspace:     workspacePath,
			Scheme:        args.Scheme,
			Configuration: args.Configuration,
			Destination:   args.Destination,
		})
		if err != nil {
			return errResult("failed to resolve build products: " + err.Error()), ShowBuildProductsOutput{}, nil
		}
		return &mcp.CallToolResult{}, ShowBuildProductsOutput{Result: result}, nil
	}))
}

func registerRunBuiltApp(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "run_built_app",
		Title:       "Run Built App",
		Description: "Build a macOS app if needed, launch the built .app bundle, and optionally wait until its first window is usable.",
		Annotations: additiveTool("Run Built App", false),
	}, SafeTool("run_built_app", func(ctx context.Context, req *mcp.CallToolRequest, args RunBuiltAppInput) (*mcp.CallToolResult, RunBuiltAppOutput, error) {
		projectPath, workspacePath, err := inferBuildLocator(ctx, req.Session, args.Project, args.Workspace)
		if err != nil {
			return errResult("failed to infer project or workspace: " + err.Error()), RunBuiltAppOutput{}, nil
		}
		opts := xcodebuild.BuildOptions{
			Project:       projectPath,
			Workspace:     workspacePath,
			Scheme:        args.Scheme,
			Configuration: args.Configuration,
			Destination:   args.Destination,
		}

		buildIfNeeded := boolWithDefault(args.BuildIfNeeded, true)
		frontmost := boolWithDefault(args.Frontmost, true)
		waitForReady := boolWithDefault(args.WaitForReady, true)
		waitForWindow := boolWithDefault(args.WaitForWindow, true)
		waitForWindowTitle := boolWithDefault(args.WaitForWindowTitle, false)
		waitForContent := boolWithDefault(args.WaitForContent, true)
		timeout := secondsWithDefault(args.TimeoutSeconds, 20*time.Second)

		var buildResult *xcodebuild.BuildResult
		products, err := xcodebuild.ShowBuildProducts(ctx, opts)
		if err != nil {
			return errResult("failed to resolve build products: " + err.Error()), RunBuiltAppOutput{}, nil
		}
		product := xcodebuild.PrimaryAppProduct(products.Products)
		if product == nil {
			return errResult("no .app product found for the selected scheme"), RunBuiltAppOutput{}, nil
		}

		if buildIfNeeded || product.BundlePath == "" || !pathExists(product.BundlePath) {
			buildResult, err = xcodebuild.Build(ctx, opts)
			if err != nil {
				return errResult("build execution failed: " + err.Error()), RunBuiltAppOutput{}, nil
			}
			product = xcodebuild.PrimaryAppProduct(buildResult.Products)
			if product == nil || product.BundlePath == "" {
				return errResult("build did not produce a launchable .app bundle"), RunBuiltAppOutput{Build: buildResult}, nil
			}
			if !buildResult.Success {
				return errResult("build failed"), RunBuiltAppOutput{Build: buildResult, Product: product}, nil
			}
		}

		runCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		runningApp, err := macosapp.Launch(runCtx, product.BundlePath, product.BundleID, frontmost)
		if err != nil {
			return errResult("launch failed: " + err.Error()), RunBuiltAppOutput{Build: buildResult, Product: product}, nil
		}

		out := RunBuiltAppOutput{Build: buildResult, Product: product}
		if waitForReady {
			ready, err := macosapp.WaitUntilReady(runCtx, macosapp.AppSelector{
				BundleID: product.BundleID,
				PID:      pidOrZero(runningApp),
			}, macosapp.WaitOptions{
				RequireWindow:      waitForWindow,
				RequireWindowTitle: waitForWindowTitle,
				RequireContent:     waitForContent,
			})
			if err != nil {
				return errResult("app launched but did not become ready: " + err.Error()), out, nil
			}
			if ready != nil {
				ready.Frontmost = frontmost
			}
			out.App = ready
			return &mcp.CallToolResult{}, out, nil
		}

		if runningApp != nil {
			out.App = &macosapp.ReadyState{
				Ready:     true,
				Name:      runningApp.Name,
				BundleID:  runningApp.BundleID,
				PID:       runningApp.PID,
				Frontmost: frontmost,
			}
		}
		return &mcp.CallToolResult{}, out, nil
	}))
}

func registerWaitForAppReady(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "wait_for_app_ready",
		Title:       "Wait For App Ready",
		Description: "Wait for a launched macOS app to have a stable process and optionally a usable first window.",
		Annotations: readOnlyTool("Wait For App Ready"),
	}, SafeTool("wait_for_app_ready", func(ctx context.Context, req *mcp.CallToolRequest, args WaitForAppReadyInput) (*mcp.CallToolResult, WaitForAppReadyOutput, error) {
		if args.BundleID == "" && args.Name == "" && args.PID == 0 {
			return errResult("bundle_id, name, or pid is required"), WaitForAppReadyOutput{}, nil
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), secondsWithDefault(args.TimeoutSeconds, 15*time.Second))
		defer cancel()

		app, err := macosapp.WaitUntilReady(waitCtx, macosapp.AppSelector{
			BundleID: args.BundleID,
			Name:     args.Name,
			PID:      args.PID,
		}, macosapp.WaitOptions{
			RequireWindow:      boolWithDefault(args.WaitForWindow, true),
			RequireWindowTitle: boolWithDefault(args.WaitForWindowTitle, false),
			RequireContent:     boolWithDefault(args.WaitForContent, true),
		})
		if err != nil {
			return errResult("wait failed: " + err.Error()), WaitForAppReadyOutput{}, nil
		}
		return &mcp.CallToolResult{}, WaitForAppReadyOutput{App: app}, nil
	}))
}

func registerRenderAllPreviews(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "render_all_previews",
		Title:       "Render All Previews",
		Description: "Render every SwiftUI preview definition in a file set and return structured results with snapshot paths.",
		Annotations: additiveTool("Render All Previews", false),
	}, SafeTool("render_all_previews", func(ctx context.Context, req *mcp.CallToolRequest, args RenderAllPreviewsInput) (*mcp.CallToolResult, RenderAllPreviewsOutput, error) {
		if args.Root == "" {
			args.Root = sessionProjectRoot(ctx, req.Session, ".")
		}
		out, err := renderAllPreviews(ctx, args)
		if err != nil {
			return errResult(err.Error()), RenderAllPreviewsOutput{}, nil
		}
		return &mcp.CallToolResult{}, out, nil
	}))
}

func renderAllPreviews(ctx context.Context, args RenderAllPreviewsInput) (RenderAllPreviewsOutput, error) {
	bridge := getXcodeBridge()
	if bridge == nil {
		return RenderAllPreviewsOutput{}, fmt.Errorf("xcode bridge is not available; enable the xcode toolset and make sure Xcode is running")
	}

	tabIdentifier := args.TabIdentifier
	if tabIdentifier == "" {
		var err error
		tabIdentifier, err = inferTabIdentifier(ctx, bridge)
		if err != nil {
			return RenderAllPreviewsOutput{}, err
		}
	}

	root := args.Root
	if root == "" {
		root = "."
	}
	files, err := collectPreviewFiles(root, args.Glob, args.Files)
	if err != nil {
		return RenderAllPreviewsOutput{}, err
	}

	tempDir, err := os.MkdirTemp("", "xcmcp-previews-*")
	if err != nil {
		return RenderAllPreviewsOutput{}, err
	}

	timeout := args.Timeout
	if timeout <= 0 {
		timeout = 120
	}

	results := make([]PreviewRenderResult, 0)
	for _, file := range files {
		localPath, sourcePath := previewPaths(root, file)
		count, err := previewDefinitionCount(localPath)
		if err != nil && os.IsNotExist(unwrapPathError(err)) {
			// Local path doesn't exist — the file may be an Xcode project
			// path (e.g. from XcodeGlob) that differs from the filesystem
			// layout (common with Swift Packages). Fall back to reading
			// through the bridge.
			count, err = bridgePreviewDefinitionCount(ctx, bridge, tabIdentifier, sourcePath)
		}
		if err != nil {
			results = append(results, PreviewRenderResult{SourceFile: sourcePath, Error: err.Error()})
			continue
		}
		for i := 0; i < count; i++ {
			result := PreviewRenderResult{SourceFile: sourcePath, PreviewIndex: i}
			callResult, err := bridge.callTool(ctx, "RenderPreview", map[string]any{
				"tabIdentifier":                tabIdentifier,
				"sourceFilePath":               sourcePath,
				"previewDefinitionIndexInFile": i,
				"timeout":                      timeout,
			})
			if err != nil {
				result.Error = err.Error()
				results = append(results, result)
				continue
			}
			image, mime, err := extractImageContent(callResult)
			if err != nil {
				result.Error = err.Error()
				results = append(results, result)
				continue
			}
			name := fmt.Sprintf("%s-%d%s", sanitizeFilename(sourcePath), i, previewFileExtension(mime))
			snapshotPath := filepath.Join(tempDir, name)
			if err := os.WriteFile(snapshotPath, image, 0o644); err != nil {
				result.Error = err.Error()
				results = append(results, result)
				continue
			}
			result.Success = true
			result.MIMEType = mime
			result.SnapshotPath = snapshotPath
			results = append(results, result)
		}
	}

	return RenderAllPreviewsOutput{
		TabIdentifier: tabIdentifier,
		Results:       results,
	}, nil
}

func inferTabIdentifier(ctx context.Context, bridge xcodeBridge) (string, error) {
	result, err := bridge.callTool(ctx, "XcodeListWindows", nil)
	if err != nil {
		return "", fmt.Errorf("list Xcode windows: %w", err)
	}
	var raw any
	if err := decodeBridgeResult(result, &raw); err != nil {
		return "", fmt.Errorf("decode Xcode windows: %w", err)
	}
	if tabIdentifier := findStringField(raw, "tabIdentifier", "tab_identifier"); tabIdentifier != "" {
		return tabIdentifier, nil
	}
	return "", fmt.Errorf("no Xcode workspace tab identifier found")
}

func findStringField(value any, keys ...string) string {
	switch value := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if s, ok := value[key].(string); ok && s != "" {
				return s
			}
		}
		for _, child := range value {
			if s := findStringField(child, keys...); s != "" {
				return s
			}
		}
	case []any:
		for _, child := range value {
			if s := findStringField(child, keys...); s != "" {
				return s
			}
		}
	}
	return ""
}

func collectPreviewFiles(root, pattern string, files []string) ([]string, error) {
	if len(files) > 0 {
		out := append([]string(nil), files...)
		sort.Strings(out)
		return out, nil
	}
	if pattern == "" {
		pattern = "**/*.swift"
	}

	var matches []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if globMatch(pattern, rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func previewPaths(root, file string) (localPath, sourcePath string) {
	sourcePath = filepath.ToSlash(file)
	if filepath.IsAbs(file) {
		localPath = file
		if rel, err := filepath.Rel(root, file); err == nil {
			sourcePath = filepath.ToSlash(rel)
		}
		return localPath, sourcePath
	}
	return filepath.Join(root, file), sourcePath
}

func previewDefinitionCount(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return len(previewTokenRE.FindAll(data, -1)), nil
}

// bridgePreviewDefinitionCount reads a source file through the Xcode bridge
// and counts #Preview / PreviewProvider definitions. This handles cases where
// Xcode project paths don't correspond to filesystem paths (e.g. Swift Packages).
func bridgePreviewDefinitionCount(ctx context.Context, bridge xcodeBridge, tabIdentifier, sourcePath string) (int, error) {
	result, err := bridge.callTool(ctx, "XcodeRead", map[string]any{
		"tabIdentifier": tabIdentifier,
		"filePath":      sourcePath,
	})
	if err != nil {
		return 0, fmt.Errorf("bridge read %s: %w", sourcePath, err)
	}
	for _, item := range result.Content {
		text, ok := item.(*mcp.TextContent)
		if ok && text.Text != "" {
			return len(previewTokenRE.FindAll([]byte(text.Text), -1)), nil
		}
	}
	return 0, fmt.Errorf("bridge read %s: empty result", sourcePath)
}

// unwrapPathError extracts the underlying error from a wrapped path error,
// allowing os.IsNotExist to work on fmt.Errorf-wrapped errors.
func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr
	}
	return err
}

func extractImageContent(result *mcp.CallToolResult) ([]byte, string, error) {
	if result == nil {
		return nil, "", fmt.Errorf("empty render result")
	}
	for _, item := range result.Content {
		image, ok := item.(*mcp.ImageContent)
		if ok && len(image.Data) > 0 {
			return image.Data, image.MIMEType, nil
		}
	}
	for _, item := range result.Content {
		text, ok := item.(*mcp.TextContent)
		if ok && text.Text != "" {
			return nil, "", errors.New(strings.TrimSpace(text.Text))
		}
	}
	return nil, "", fmt.Errorf("render preview did not return image content")
}

func previewFileExtension(mime string) string {
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png", "":
		return ".png"
	default:
		return ".img"
	}
}

func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "_", ":", "-")
	return replacer.Replace(s)
}

func globMatch(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	for _, candidate := range []string{pattern, strings.TrimPrefix(pattern, "**/")} {
		regex := regexp.QuoteMeta(candidate)
		regex = strings.ReplaceAll(regex, `\*\*`, `.*`)
		regex = strings.ReplaceAll(regex, `\*`, `[^/]*`)
		regex = strings.ReplaceAll(regex, `\?`, `.`)
		ok, _ := regexp.MatchString("^"+regex+"$", path)
		if ok {
			return true
		}
	}
	return false
}

func boolWithDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func secondsWithDefault(value float64, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return time.Duration(value * float64(time.Second))
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func pidOrZero(app *macosapp.RunningApp) int {
	if app == nil {
		return 0
	}
	return app.PID
}
