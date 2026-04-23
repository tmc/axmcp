package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/axmcp/internal/ui"
	"golang.org/x/sys/unix"
)

const defaultCLIVisibilityDelay = 400 * time.Millisecond

var cliVisibilityState struct {
	enabled atomic.Bool
	pending atomic.Bool
	delayNS atomic.Int64
}

// isTTY reports whether stdin is an interactive terminal.
func isTTY() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TIOCGETA)
	return err == nil
}

func setCLIVisibility(enabled bool, delay time.Duration) {
	if delay < 0 {
		delay = 0
	}
	cliVisibilityState.enabled.Store(enabled)
	cliVisibilityState.delayNS.Store(int64(delay))
	if !enabled {
		cliVisibilityState.pending.Store(false)
	}
}

func noteCLIVisualFeedback() {
	if !cliVisibilityState.enabled.Load() {
		return
	}
	cliVisibilityState.pending.Store(true)
}

func clearCLIVisualFeedback() {
	cliVisibilityState.pending.Store(false)
}

func cliVisibilityEnabled() bool {
	return cliVisibilityState.enabled.Load()
}

func waitForCLIVisualFeedback() {
	if !cliVisibilityEnabled() || !cliVisibilityState.pending.Load() {
		return
	}
	delay := time.Duration(cliVisibilityState.delayNS.Load())
	if delay > 0 {
		time.Sleep(delay)
	}
	clearCLIVisualFeedback()
}

// runCLI runs the interactive CLI mode and does not return on success.
func runCLI() {
	visibility := true
	visibilityDelay := defaultCLIVisibilityDelay
	setCLIVisibility(visibility, visibilityDelay)

	root := &cobra.Command{
		Use:   "axmcp",
		Short: "Accessibility automation CLI (MCP server when stdin is not a TTY)",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			setCLIVisibility(visibility, visibilityDelay)
		},
	}
	// -v is handled early in main() before macgo; declare here so cobra
	// doesn't reject it as an unknown flag.
	root.PersistentFlags().BoolP("verbose", "v", false, "enable verbose debug logging")
	root.PersistentFlags().Bool("ghost-cursor", true, "draw the ghost cursor overlay for pointer actions")
	root.PersistentFlags().Bool("eyecandy", true, "enable ghost cursor eyecandy effects")
	root.PersistentFlags().BoolVar(&visibility, "visibility", true, "keep the CLI alive briefly so visual feedback is perceptible")
	root.PersistentFlags().DurationVar(&visibilityDelay, "visibility-delay", defaultCLIVisibilityDelay, "how long to linger after visual feedback before exiting")

	root.AddCommand(
		cliApps(),
		cliTree(),
		cliFind(),
		cliPipe(),
		cliClick(),
		cliHover(),
		cliRightClick(),
		cliType(),
		cliMenu(),
		cliFocus(),
		cliScreenshot(),
	)

	if err := root.Execute(); err != nil {
		ui.WaitForWindows()
		os.Exit(1)
	}
	waitForCLIVisualFeedback()
	ui.WaitForWindows()
	os.Exit(0)
}

// ── apps ──────────────────────────────────────────────────────────────────────

func cliApps() *cobra.Command {
	return &cobra.Command{
		Use:   "apps",
		Short: "List running macOS applications",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := exec.Command("lsappinfo", "list").Output()
			if err != nil {
				return fmt.Errorf("lsappinfo: %w", err)
			}
			// Reuse the same parsing logic as the MCP tool by writing to stdout directly.
			os.Stdout.Write(parseAppsTable(out))
			return nil
		},
	}
}

func parseAppsTable(lsout []byte) []byte {
	type entry struct {
		name, bid string
		pid       int
	}
	var apps []entry
	var cur entry
	for _, line := range strings.Split(string(lsout), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, ") \"") && strings.Contains(line, "ASN:") {
			if cur.bid != "" {
				apps = append(apps, cur)
			}
			cur = entry{}
			s := line[strings.Index(line, "\"")+1:]
			cur.name = s[:strings.Index(s, "\"")]
		} else if strings.HasPrefix(line, "bundleID=") {
			id := strings.Trim(strings.TrimPrefix(line, "bundleID="), `"`)
			if id != "[ NULL ]" {
				cur.bid = id
			}
		} else if strings.HasPrefix(line, "pid = ") {
			rest := strings.TrimPrefix(line, "pid = ")
			if i := strings.IndexAny(rest, " \t"); i > 0 {
				rest = rest[:i]
			}
			cur.pid, _ = strconv.Atoi(rest)
		}
	}
	if cur.bid != "" {
		apps = append(apps, cur)
	}
	var buf bytes.Buffer
	for _, a := range apps {
		fmt.Fprintf(&buf, "%-45s  pid=%-6d  %s\n", a.bid, a.pid, a.name)
	}
	return buf.Bytes()
}

// ── tree ──────────────────────────────────────────────────────────────────────

func cliTree() *cobra.Command {
	var depth int
	cmd := &cobra.Command{
		Use:   "tree <app>",
		Short: "Print the AX element tree for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			if depth <= 0 {
				depth = 4
			}
			fmt.Print(treeText(app.Root(), 0, depth))
			return nil
		},
	}
	cmd.Flags().IntVarP(&depth, "depth", "d", 4, "max tree depth")
	return cmd
}

// ── find ──────────────────────────────────────────────────────────────────────

func cliFind() *cobra.Command {
	var role, title, contains string
	var limit int
	cmd := &cobra.Command{
		Use:   "find <app>",
		Short: "Find AX elements by role or normalized text lookup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			if limit <= 0 {
				limit = 500
			}
			result := findElements(app.Root(), searchOptions{
				Role:     role,
				Title:    title,
				Contains: contains,
				Limit:    limit,
			})
			if len(result.matches) == 0 {
				fmt.Println(noMatchMessage(result))
				return nil
			}
			if note := selectionReason(result); note != "" {
				fmt.Println(note)
			}
			for i, match := range result.matches {
				fmt.Printf("[%d] %s\n", i, formatMatch(match))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "AX role filter (e.g. AXButton)")
	cmd.Flags().StringVar(&title, "title", "", "exact text match across title, desc, value, and identifier")
	cmd.Flags().StringVar(&contains, "contains", "", "substring match across title, desc, value, and identifier")
	cmd.Flags().IntVar(&limit, "limit", 50, "max results")
	return cmd
}

// ── pipe ──────────────────────────────────────────────────────────────────────

func cliPipe() *cobra.Command {
	return &cobra.Command{
		Use:   "pipe <pipeline>",
		Short: "Execute an AX pipeline (stages separated by //)",
		Long: `Execute an accessibility pipeline.

Stages are separated by // (double-slash).
Available stages:
  app <bundle-id|pid>         Open application
  window [substr]             Focus first matching window
  windows                     List windows (app or global)
  focus                       Get focused element
  raise                       Bring element to front
  children                    Get children of element
  first                       Take first element from list
  find [flags]                Search descendants
        --role R              Filter by AXRole
        --title T             Exact text match across title, desc, value, identifier
        --contains C          Substring match across title, desc, value, identifier
        --id I                Exact identifier filter
  .                           Print current context
  tree [--depth N]            Print element tree
  list                        Print element list
  json                        Print JSON output
  click                       Click element
  rightclick                  Right-click element
  click-at <x> <y>            Click at offset
  hover                       Move mouse to element
  screenshot [--out PATH]     Capture current scope and save a PNG artifact
        [--padding N]         Expand element captures by N pixels on each side
  ocr-hover <text>            Move mouse to OCR text
  highlight <text>            Draw a 2s highlight around OCR text
  type <text>                 Type text
  attr <AXAttr>               Print attribute
  click-menu <A> <B> <C>      Click menu path`,
		Example: strings.Join([]string{
			`  axmcp pipe "app com.apple.dt.Xcode // windows"`,
			`  axmcp pipe "app com.apple.finder // focus // attr AXTitle"`,
		}, "\n"),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := execPipeline(strings.Join(args, " "))
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	}
}

// ── click ─────────────────────────────────────────────────────────────────────

func cliClick() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "click <app> <contains>",
		Short: "Click an element found by normalized text lookup",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			result := findElements(app.Root(), searchOptions{
				Role:     role,
				Contains: args[1],
				Limit:    500,
			})
			if len(result.matches) == 0 {
				return performCLIOCRClick(app, args[1])
			}
			resolution := resolveClickTarget(result.matches[0], 500)
			if resolution.target.element == nil {
				return fmt.Errorf("target disappeared: %s", formatMatch(result.matches[0]))
			}
			clickSummary, err := performDefaultClick(resolution.target)
			if err != nil {
				return fmt.Errorf("click %s: %w", formatSnapshot(resolution.target), err)
			}
			fmt.Println(clickSummary)
			printCLIActionNotes(result, resolution)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "AX role filter")
	return cmd
}

// ── hover ─────────────────────────────────────────────────────────────────────

func cliHover() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "hover <app> <contains>",
		Short: "Hover an element found by normalized text lookup",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			result := findElements(app.Root(), searchOptions{
				Role:     role,
				Contains: args[1],
				Limit:    500,
			})
			if len(result.matches) == 0 {
				return performCLIOCRHover(app, args[1])
			}
			resolution := resolveClickTarget(result.matches[0], 500)
			if resolution.target.element == nil {
				return fmt.Errorf("target disappeared: %s", formatMatch(result.matches[0]))
			}
			hoverSummary, err := performDefaultHover(resolution.target)
			if err != nil {
				return fmt.Errorf("hover %s: %w", formatSnapshot(resolution.target), err)
			}
			fmt.Println(hoverSummary)
			printCLIActionNotes(result, resolution)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "AX role filter")
	return cmd
}

// ── rightclick ────────────────────────────────────────────────────────────────

func cliRightClick() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "rightclick <app> <contains>",
		Short: "Right-click an element found by normalized text lookup",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			result, resolution, err := resolveCLIActionTarget(app, role, args[1])
			if err != nil {
				return err
			}
			rightClickSummary, err := performDefaultRightClick(resolution.target)
			if err != nil {
				return fmt.Errorf("rightclick %s: %w", formatSnapshot(resolution.target), err)
			}
			fmt.Println(rightClickSummary)
			printCLIActionNotes(result, resolution)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "AX role filter")
	return cmd
}

func resolveCLIActionTarget(app *axuiautomation.Application, role, contains string) (matchResult, clickResolution, error) {
	result := findElements(app.Root(), searchOptions{
		Role:     role,
		Contains: contains,
		Limit:    500,
	})
	if len(result.matches) == 0 {
		return result, clickResolution{}, fmt.Errorf("%s", noMatchMessage(result))
	}
	match := result.matches[0]
	resolution := resolveClickTarget(match, 500)
	if resolution.target.element == nil {
		return result, resolution, fmt.Errorf("target disappeared: %s", formatMatch(match))
	}
	return result, resolution, nil
}

func printCLIActionNotes(result matchResult, resolution clickResolution) {
	if note := selectionReason(result); note != "" {
		fmt.Println(note)
	}
	if resolution.reason != "" {
		fmt.Println(resolution.reason)
	}
}

func performCLIOCRHover(app *axuiautomation.Application, query string) error {
	capture, err := capturePipelineOCRScope(&pipeContext{app: app})
	if err != nil {
		return err
	}

	selection, err := selectOCRMatch(capture.result, query, nil)
	if err != nil {
		return err
	}
	summary, resolutionNote, err := performOCRHover(capture, selection.match)
	if err != nil {
		return err
	}
	fmt.Println(summary)
	fmt.Println(selection.resolved)
	if resolutionNote != "" {
		fmt.Println(resolutionNote)
	}
	return nil
}

func performCLIOCRClick(app *axuiautomation.Application, query string) error {
	capture, err := capturePipelineOCRScope(&pipeContext{app: app})
	if err != nil {
		return err
	}

	selection, err := selectOCRMatch(capture.result, query, nil)
	if err != nil {
		return err
	}
	x, y := selection.match.Center()
	if err := clickLocalPoint(capture.target, x, y); err != nil {
		return fmt.Errorf("click OCR match %q in %s: %w", selection.match.Text, capture.desc, err)
	}
	fmt.Printf("clicked OCR match %q in %s at %d,%d via local click\n", selection.match.Text, capture.desc, x, y)
	fmt.Println(selection.resolved)
	return nil
}

// ── type ──────────────────────────────────────────────────────────────────────

func cliType() *cobra.Command {
	return &cobra.Command{
		Use:   "type <app> <contains> <text>",
		Short: "Type text into an element found by normalized text lookup",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			result := findElements(app.Root(), searchOptions{
				Contains: args[1],
				Limit:    500,
			})
			if len(result.matches) == 0 {
				return fmt.Errorf("%s", noMatchMessage(result))
			}
			el := result.matches[0].snapshot.element
			if el == nil {
				return fmt.Errorf("type target disappeared: %s", formatMatch(result.matches[0]))
			}
			if err := focusElement(el); err != nil {
				return fmt.Errorf("focus %s: %w", formatMatch(result.matches[0]), err)
			}
			endTypingCursor := beginTypingCursor(el)
			defer endTypingCursor()
			if err := el.TypeText(args[2]); err != nil {
				return fmt.Errorf("type %s: %w", formatMatch(result.matches[0]), err)
			}
			fmt.Printf("typed into %s\n", formatMatch(result.matches[0]))
			if note := selectionReason(result); note != "" {
				fmt.Println(note)
			}
			return nil
		},
	}
}

// ── menu ──────────────────────────────────────────────────────────────────────

func cliMenu() *cobra.Command {
	return &cobra.Command{
		Use:     "menu <app> <item> [item...]",
		Short:   "Click a menu item by path",
		Example: `  axmcp menu com.apple.dt.Xcode File New "Xcode Project..."`,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			path := args[1:]
			if err := app.ClickMenuItem(path); err != nil {
				return fmt.Errorf("menu: %w", err)
			}
			fmt.Println("clicked menu:", strings.Join(path, " > "))
			return nil
		},
	}
}

// ── focus ─────────────────────────────────────────────────────────────────────

func cliFocus() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "focus <app>",
		Short: "Get the currently focused AX element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()
			el := app.FocusedElement()
			if el == nil {
				return fmt.Errorf("no focused element")
			}
			if asJSON {
				return json.NewEncoder(os.Stdout).Encode(elementAttrs(el))
			}
			fmt.Println(elementSummary(el))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return cmd
}

// ── screenshot ────────────────────────────────────────────────────────────────

func cliScreenshot() *cobra.Command {
	var contains, role, out string
	cmd := &cobra.Command{
		Use:   "screenshot <app>",
		Short: "Capture a screenshot of a window or element",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			isElement := contains != "" || role != ""

			// For full-window screenshots, try CGWindowListCreateImage first.
			// This runs synchronously and avoids the SCK dispatch issue.
			if !isElement {
				diagf("screenshot: trying CGWindowList capture (no AX)\n")
				png, err := captureWindowByName(args[0])
				if err == nil {
					diagf("screenshot: capture success, writing output\n")
					return writeScreenshot(out, png)
				}
				diagf("screenshot: capture failed: %v, falling back to AX\n", err)
			}

			// Screen Recording is required for the AX/SCK fallback paths.
			diagf("screenshot: checking screen recording permission\n")
			if !ui.WaitForScreenRecording(30 * time.Second) {
				return fmt.Errorf("Screen Recording is still not granted — enable axmcp.app in System Settings > Privacy & Security and retry")
			}

			diagf("screenshot: opening %s via AX\n", args[0])
			axuiautomation.SpinRunLoop(200 * time.Millisecond)
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()

			var el *axuiautomation.Element
			if isElement {
				q := app.Descendants().WithLimit(100)
				if role != "" {
					q = q.ByRole(role)
				}
				if contains != "" {
					q = q.ByTitleContains(contains)
				}
				el = q.First()
				if el == nil {
					return fmt.Errorf("element not found")
				}
			} else {
				wins := app.WindowList()
				diagf("screenshot: found %d AX windows\n", len(wins))
				if len(wins) > 0 {
					el = wins[0]
				}
			}

			if el == nil {
				return fmt.Errorf("no windows found for %q", args[0])
			}

			diagf("screenshot: capturing element/window\n")
			png, err := captureElementOrWindow(args[0], isElement, el)
			if err != nil {
				return err
			}
			return writeScreenshot(out, png)
		},
	}
	cmd.Flags().StringVar(&contains, "contains", "", "element title substring")
	cmd.Flags().StringVar(&role, "role", "", "AX role filter")
	cmd.Flags().StringVarP(&out, "out", "o", "", "output file (default: screenshot-<unix>.png)")
	return cmd
}

func writeScreenshot(out string, png []byte) error {
	dest := out
	if dest == "" {
		dest = "screenshot-" + strconv.FormatInt(time.Now().Unix(), 10) + ".png"
	}
	if err := os.WriteFile(dest, png, 0644); err != nil {
		return err
	}
	fmt.Println("saved:", dest)
	return nil
}
