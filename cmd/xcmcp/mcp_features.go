package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/axmcp/internal/project"
)

var (
	swiftUIViewNamesRE = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:(?:public|internal|fileprivate|private|final)\s+)*struct\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*[^\n{]*\bView\b`)
	previewVariants    = []string{"light", "dark", "loading", "error", "empty", "largeContent", "rtl", "dynamicType"}
)

func serverInstructions(enableXcode bool, prefix string) string {
	var b strings.Builder
	b.WriteString("xcmcp exposes project inspection, build, simulator, app, and Xcode automation features over MCP.")
	b.WriteString(" Prefer MCP tools, prompts, and resources over shell commands for routine discovery work.")
	b.WriteString(" If the client provides file roots, treat those roots as the workspace scope for project discovery, path suggestions, and default path resolution.")
	b.WriteString(" Use the `swift_conventions` prompt for Swift coding conventions when writing or reviewing Swift code.")
	if !enableXcode {
		return b.String()
	}

	doc := xcodeToolName(prefix, "DocumentationSearch")
	build := xcodeToolName(prefix, "BuildProject")
	issues := xcodeToolName(prefix, "XcodeRefreshCodeIssuesInFile")
	snippet := xcodeToolName(prefix, "ExecuteSnippet")

	b.WriteString("\n\nWhen Xcode bridge tools are enabled, prefer them for IDE-backed work.")
	b.WriteString(" Use `")
	b.WriteString(doc)
	b.WriteString("` for Apple framework documentation, especially for new or fast-moving APIs such as SwiftUI, Liquid Glass, and FoundationModels.")
	b.WriteString(" Use `")
	b.WriteString(build)
	b.WriteString("` for full Xcode builds, `")
	b.WriteString(issues)
	b.WriteString("` for fast file diagnostics, and `")
	b.WriteString(snippet)
	b.WriteString("` for lightweight experimentation in project context.")
	b.WriteString(" Limit changes to the requested task and avoid unrelated edits.")
	return b.String()
}

func xcodeToolName(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "_" + name
}

func completionHandler(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	if req == nil || req.Params == nil || req.Params.Ref == nil {
		return emptyCompletion(), nil
	}
	switch req.Params.Ref.Type {
	case "ref/prompt":
		return completePromptArgument(ctx, req), nil
	case "ref/resource":
		return completeResourceArgument(ctx, req), nil
	default:
		return emptyCompletion(), nil
	}
}

func emptyCompletion() *mcp.CompleteResult {
	return &mcp.CompleteResult{
		Completion: mcp.CompletionResultDetails{Values: []string{}},
	}
}

func completePromptArgument(ctx context.Context, req *mcp.CompleteRequest) *mcp.CompleteResult {
	ref := req.Params.Ref
	arg := req.Params.Argument
	if ref.Name != swiftUIPreviewPromptName {
		return emptyCompletion()
	}
	switch arg.Name {
	case "path":
		return &mcp.CompleteResult{
			Completion: mcp.CompletionResultDetails{
				Values: completePathSuggestions(ctx, req.Session, arg.Value, ".swift", 50),
			},
		}
	case "type_name":
		path := ""
		if req.Params.Context != nil {
			path = req.Params.Context.Arguments["path"]
		}
		return &mcp.CompleteResult{
			Completion: mcp.CompletionResultDetails{
				Values: completeTypeNames(path, arg.Value),
			},
		}
	case "variants":
		return &mcp.CompleteResult{
			Completion: mcp.CompletionResultDetails{
				Values: completeCSVValues(arg.Value, previewVariants),
			},
		}
	default:
		return emptyCompletion()
	}
}

func completeResourceArgument(context.Context, *mcp.CompleteRequest) *mcp.CompleteResult {
	return emptyCompletion()
}

func completePathSuggestions(ctx context.Context, session *mcp.ServerSession, value, wantExt string, limit int) []string {
	roots := sessionFileRoots(ctx, session)
	if len(roots) == 0 {
		if wd, err := os.Getwd(); err == nil {
			roots = []string{wd}
		} else {
			roots = []string{"."}
		}
	}

	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				switch {
				case strings.HasPrefix(name, "."):
					return filepath.SkipDir
				case name == "DerivedData", name == "build", name == "Pods", name == "Carthage":
					return filepath.SkipDir
				}
				return nil
			}
			if wantExt != "" && filepath.Ext(path) != wantExt {
				return nil
			}
			if !matchesPathValue(path, value) || seen[path] {
				return nil
			}
			seen[path] = true
			out = append(out, path)
			if len(out) >= limit {
				return fmt.Errorf("limit reached")
			}
			return nil
		})
		if len(out) >= limit {
			break
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func matchesPathValue(path, value string) bool {
	if value == "" {
		return true
	}
	path = filepath.Clean(path)
	value = strings.ToLower(value)
	if strings.ContainsAny(value, `/\`) {
		return strings.Contains(strings.ToLower(path), value)
	}
	return strings.HasPrefix(strings.ToLower(filepath.Base(path)), value)
}

func completeTypeNames(path, value string) []string {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, match := range swiftUIViewNamesRE.FindAllStringSubmatch(string(data), -1) {
		if len(match) < 2 {
			continue
		}
		name := match[1]
		if seen[name] || (value != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(value))) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func completeCSVValues(value string, candidates []string) []string {
	parts := strings.Split(value, ",")
	current := strings.TrimSpace(parts[len(parts)-1])
	used := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSpace(part)
		if part != "" {
			used[strings.ToLower(part)] = true
		}
	}
	prefix := strings.Join(parts[:len(parts)-1], ",")
	if prefix != "" {
		prefix += ", "
	}

	var out []string
	for _, candidate := range candidates {
		if used[strings.ToLower(candidate)] {
			continue
		}
		if current != "" && !strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(current)) {
			continue
		}
		out = append(out, prefix+candidate)
	}
	return out
}

func sessionProjectRoot(ctx context.Context, session *mcp.ServerSession, fallback string) string {
	roots := sessionFileRoots(ctx, session)
	if len(roots) > 0 {
		return roots[0]
	}
	if fallback == "" {
		return "."
	}
	return fallback
}

func sessionFileRoots(ctx context.Context, session *mcp.ServerSession) []string {
	if session == nil {
		return nil
	}
	rootCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()

	result, err := session.ListRoots(rootCtx, nil)
	if err != nil || result == nil {
		return nil
	}
	var roots []string
	for _, root := range result.Roots {
		if root == nil {
			continue
		}
		path, ok := fileRootPath(root.URI)
		if !ok {
			continue
		}
		roots = append(roots, path)
	}
	sort.Strings(roots)
	return roots
}

func fileRootPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", false
	}
	return filepath.Clean(u.Path), true
}

func inferProjectPath(ctx context.Context, session *mcp.ServerSession, path string) (string, error) {
	if path != "" {
		return path, nil
	}
	root := sessionProjectRoot(ctx, session, ".")
	projectPath, workspacePath, err := inferProjectLocator(root)
	if err != nil {
		return "", err
	}
	if workspacePath != "" {
		return workspacePath, nil
	}
	return projectPath, nil
}

func inferBuildLocator(ctx context.Context, session *mcp.ServerSession, projectPath, workspacePath string) (string, string, error) {
	if projectPath != "" || workspacePath != "" {
		return projectPath, workspacePath, nil
	}
	root := sessionProjectRoot(ctx, session, ".")
	return inferProjectLocator(root)
}

func inferProjectLocator(root string) (string, string, error) {
	root = filepath.Clean(root)
	switch filepath.Ext(root) {
	case ".xcodeproj":
		return root, "", nil
	case ".xcworkspace":
		return "", root, nil
	}

	projects, err := project.Discover(root)
	if err != nil {
		return "", "", fmt.Errorf("discover projects under %s: %w", root, err)
	}

	var projectPaths []string
	var workspacePaths []string
	for _, p := range projects {
		switch p.Type {
		case project.TypeProject:
			projectPaths = append(projectPaths, p.Path)
		case project.TypeWorkspace:
			workspacePaths = append(workspacePaths, p.Path)
		}
	}

	switch {
	case len(workspacePaths) == 1:
		return "", workspacePaths[0], nil
	case len(workspacePaths) == 0 && len(projectPaths) == 1:
		return projectPaths[0], "", nil
	case len(projectPaths)+len(workspacePaths) == 0:
		return "", "", fmt.Errorf("no Xcode project or workspace found under %s", root)
	default:
		return "", "", fmt.Errorf("ambiguous Xcode root %s: found %d workspaces and %d projects", root, len(workspacePaths), len(projectPaths))
	}
}

func readOnlyTool(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:          title,
		ReadOnlyHint:   true,
		IdempotentHint: true,
		OpenWorldHint:  boolPtr(false),
	}
}

func additiveTool(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		IdempotentHint:  idempotent,
		OpenWorldHint:   boolPtr(false),
		DestructiveHint: boolPtr(false),
	}
}

func boolPtr(v bool) *bool {
	return &v
}
