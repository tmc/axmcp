package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/xcmcp/internal/ui"
)

// isTTY reports whether stdin is an interactive terminal.
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// runCLI runs the interactive CLI mode and does not return on success.
func runCLI() {
	root := &cobra.Command{
		Use:   "axmcp",
		Short: "Accessibility automation CLI (MCP server when stdin is not a TTY)",
	}

	root.AddCommand(
		cliApps(),
		cliTree(),
		cliFind(),
		cliPipe(),
		cliClick(),
		cliType(),
		cliMenu(),
		cliFocus(),
		cliScreenshot(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
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
				limit = 50
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
  click-at <x> <y>            Click at offset
  hover                       Move mouse to element
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
				Limit:    100,
			})
			if len(result.matches) == 0 {
				return fmt.Errorf("%s", noMatchMessage(result))
			}
			match := result.matches[0]
			resolution := resolveClickTarget(match, 50)
			el := resolution.target.element
			if el == nil {
				return fmt.Errorf("click target disappeared: %s", formatMatch(match))
			}
			if err := el.Click(); err != nil {
				return fmt.Errorf("click %s: %w", formatSnapshot(resolution.target), err)
			}
			fmt.Printf("clicked %s\n", formatSnapshot(resolution.target))
			if note := selectionReason(result); note != "" {
				fmt.Println(note)
			}
			if resolution.reason != "" {
				fmt.Println(resolution.reason)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "", "AX role filter")
	return cmd
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
				Limit:    100,
			})
			if len(result.matches) == 0 {
				return fmt.Errorf("%s", noMatchMessage(result))
			}
			el := result.matches[0].snapshot.element
			if el == nil {
				return fmt.Errorf("type target disappeared: %s", formatMatch(result.matches[0]))
			}
			if err := el.Click(); err != nil {
				return fmt.Errorf("focus %s: %w", formatMatch(result.matches[0]), err)
			}
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
			axuiautomation.SpinRunLoop(200 * time.Millisecond)
			app, err := spinAndOpen(args[0])
			if err != nil {
				return err
			}
			defer app.Close()

			fmt.Printf("[DEBUG] AXIsProcessTrusted: %v\n", axuiautomation.AXIsProcessTrusted())
			fmt.Printf("[DEBUG] Connected to %s (PID: %d)\n", args[0], app.PID())
			children := app.Root().Children()
			fmt.Printf("[DEBUG] Root has %d children\n", len(children))

			var el *axuiautomation.Element
			if contains != "" || role != "" {
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
				fmt.Printf("[DEBUG] Found %d windows via WindowList()\n", len(wins))
				if len(wins) == 0 {
					return fmt.Errorf("no windows found")
				}
				el = wins[0]
			}

			png, err := captureElementOrWindow(args[0], contains != "" || role != "", el)
			if err != nil {
				return err
			}

			dest := out
			if dest == "" {
				dest = "screenshot-" + strconv.FormatInt(time.Now().Unix(), 10) + ".png"
			}
			if err := os.WriteFile(dest, png, 0644); err != nil {
				return err
			}
			fmt.Println("saved:", dest)
			return nil
		},
	}
	cmd.Flags().StringVar(&contains, "contains", "", "element title substring")
	cmd.Flags().StringVar(&role, "role", "", "AX role filter")
	cmd.Flags().StringVarP(&out, "out", "o", "", "output file (default: screenshot-<unix>.png)")
	return cmd
}
