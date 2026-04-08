package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/axmcp/internal/sdef"
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
		Use:   "ascriptmcp",
		Short: "AppleScript automation CLI (MCP server when stdin is not a TTY)",
	}

	root.AddCommand(
		cliListApps(),
		cliExposeApp(),
		cliRun(),
		cliGet(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// ── list-apps ─────────────────────────────────────────────────────────────────

func cliListApps() *cobra.Command {
	return &cobra.Command{
		Use:   "list-apps",
		Short: "List scriptable macOS applications",
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := os.ReadDir("/Applications")
			if err != nil {
				return fmt.Errorf("read /Applications: %w", err)
			}
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".app") {
					continue
				}
				fmt.Println(e.Name())
			}
			return nil
		},
	}
}

// ── expose ────────────────────────────────────────────────────────────────────

func cliExposeApp() *cobra.Command {
	return &cobra.Command{
		Use:   "expose <app>",
		Short: "List all AppleScript commands available for an app",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := resolveAppPath(args[0])
			d, err := sdef.Parse(appPath)
			if err != nil {
				return err
			}
			appName := sdef.AppName(appPath)
			for _, c := range d.Commands() {
				toolName := sdef.ToolName(appName, c.Name)
				desc := c.Description
				if desc == "" {
					desc = c.Name
				}
				fmt.Printf("%-40s  %s\n", toolName, desc)
			}
			return nil
		},
	}
}

// ── run ───────────────────────────────────────────────────────────────────────

func cliRun() *cobra.Command {
	var object string
	var params []string
	cmd := &cobra.Command{
		Use:   "run <app> <command> [direct-param]",
		Short: "Run an AppleScript command on an app",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := resolveAppPath(args[0])
			appName := sdef.AppName(appPath)
			cmdName := args[1]
			direct := ""
			if len(args) == 3 {
				direct = args[2]
			}
			paramMap := map[string]string{}
			for _, kv := range params {
				k, v, ok := strings.Cut(kv, "=")
				if !ok {
					return fmt.Errorf("invalid param %q: expected key=value", kv)
				}
				paramMap[k] = v
			}

			var script string
			if object != "" {
				var b strings.Builder
				fmt.Fprintf(&b, "tell application %q\n", appName)
				fmt.Fprintf(&b, "\ttell %s\n\t\t%s", object, cmdName)
				if direct != "" {
					fmt.Fprintf(&b, " %s", direct)
				}
				for k, v := range paramMap {
					fmt.Fprintf(&b, " %s %s", k, v)
				}
				fmt.Fprintf(&b, "\n\tend tell\nend tell")
				script = b.String()
			} else {
				script = sdef.BuildScript(appName, cmdName, direct, paramMap)
			}

			result, err := sdef.RunScript(script)
			if err != nil {
				return err
			}
			fmt.Println(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&object, "object", "", `target object expression, e.g. "front workspace document"`)
	cmd.Flags().StringArrayVar(&params, "param", nil, "named parameter as key=value (repeatable)")
	return cmd
}

// ── get ───────────────────────────────────────────────────────────────────────

func cliGet() *cobra.Command {
	var object string
	cmd := &cobra.Command{
		Use:   "get <app> <property>",
		Short: "Get a property of an app or object via AppleScript",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := resolveAppPath(args[0])
			appName := sdef.AppName(appPath)
			script := sdef.GetPropertyScript(appName, object, args[1])
			result, err := sdef.RunScript(script)
			if err != nil {
				return err
			}
			fmt.Println(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&object, "object", "", `target object expression`)
	return cmd
}
