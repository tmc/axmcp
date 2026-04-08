// Command ascript is a CLI for exploring and running AppleScript commands
// against scriptable macOS applications.
//
// It parses the app's sdef (scripting definition) and provides subcommands
// to list available commands/classes and run individual commands via osascript.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/macgo"
	"github.com/tmc/axmcp/internal/sdef"
)

func main() {
	cfg := macgo.NewConfig().
		WithAppName("ascript").
		WithCustom("com.apple.security.automation.apple-events").
		WithAdHocSign()
	cfg.BundleID = "dev.tmc.ascript"
	if err := macgo.Start(cfg); err != nil {
		log.Fatalf("macgo: %v", err)
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ascript",
	Short: "Explore and run AppleScript commands against macOS applications",
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON")
	rootCmd.AddCommand(listCmd, classesCmd, runCmd, scriptCmd, getCmd)
}

func jsonFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// completeApps completes /Applications/*.app paths (arg 0).
func completeApps(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, err := os.ReadDir("/Applications")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".app") {
			continue
		}
		path := "/Applications/" + e.Name()
		if strings.HasPrefix(strings.ToLower(path), strings.ToLower(toComplete)) {
			out = append(out, path)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeCommands completes app path (arg 0) then command names from sdef (arg 1).
func completeCommands(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeApps(nil, nil, toComplete)
	}
	if len(args) == 1 {
		d, err := sdef.Parse(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, c := range d.Commands() {
			if strings.HasPrefix(c.Name, toComplete) {
				out = append(out, c.Name)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	// arg 2+: complete param names from the command's sdef entry as "key="
	if len(args) >= 2 {
		d, err := sdef.Parse(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cmdName := args[1]
		for _, c := range d.Commands() {
			if c.Name != cmdName {
				continue
			}
			var out []string
			if c.DirectParameter != nil && c.DirectParameter.Type != "" {
				if strings.HasPrefix("direct=", toComplete) {
					out = append(out, "direct=")
				}
			}
			for _, p := range c.Parameters {
				key := p.Name + "="
				if strings.HasPrefix(key, toComplete) {
					out = append(out, key)
				}
			}
			return out, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
		}
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// completeProperties completes app path (arg 0) then property names (arg 1).
func completeProperties(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeApps(nil, nil, toComplete)
	}
	if len(args) == 1 {
		d, err := sdef.Parse(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		seen := map[string]bool{}
		var out []string
		for _, cl := range d.Classes() {
			for _, p := range cl.Properties {
				if seen[p.Name] {
					continue
				}
				if strings.HasPrefix(p.Name, toComplete) {
					seen[p.Name] = true
					out = append(out, p.Name)
				}
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// ── ascript list ──────────────────────────────────────────────────────────────

var listCmd = &cobra.Command{
	Use:               "list <app-path>",
	Short:             "List all scriptable commands for an application",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeApps,
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := sdef.Parse(args[0])
		if err != nil {
			return err
		}
		cmds := d.Commands()
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(cmds)
		}
		for _, c := range cmds {
			params := make([]string, 0, len(c.Parameters))
			for _, p := range c.Parameters {
				if p.Optional == "yes" {
					params = append(params, "["+p.Name+" "+p.Type+"]")
				} else {
					params = append(params, p.Name+" "+p.Type)
				}
			}
			if c.DirectParameter != nil && c.DirectParameter.Type != "" {
				params = append([]string{"<" + c.DirectParameter.Type + ">"}, params...)
			}
			line := c.Name
			if len(params) > 0 {
				line += " " + strings.Join(params, " ")
			}
			if c.Description != "" {
				fmt.Printf("%-50s  -- %s\n", line, c.Description)
			} else {
				fmt.Println(line)
			}
		}
		return nil
	},
}

// ── ascript classes ───────────────────────────────────────────────────────────

var classesCmd = &cobra.Command{
	Use:               "classes <app-path>",
	Short:             "List all scriptable classes for an application",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeApps,
	RunE: func(cmd *cobra.Command, args []string) error {
		d, err := sdef.Parse(args[0])
		if err != nil {
			return err
		}
		classes := d.Classes()
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(classes)
		}
		for _, c := range classes {
			inherits := ""
			if c.Inherits != "" {
				inherits = " (inherits " + c.Inherits + ")"
			}
			fmt.Printf("%-35s%s\n", c.Name+inherits, "  -- "+c.Description)
			for _, p := range c.Properties {
				access := p.Access
				if access == "" {
					access = "rw"
				}
				fmt.Printf("  .%-30s %s [%s]\n", p.Name, p.Type, access)
			}
		}
		return nil
	},
}

// ── ascript run ───────────────────────────────────────────────────────────────

var runCmd = &cobra.Command{
	Use:   "run <app-path> <command> [param=value...]",
	Short: "Run a scriptable command on an application",
	Long: `Run an AppleScript command against a scriptable application.

Parameters are specified as key=value pairs. Use 'direct=value' for the
direct parameter. Values are passed as AppleScript literals — quote strings
with double-quotes if needed.

Examples:
  ascript run /Applications/Xcode.app build
  ascript run /Applications/Xcode.app quit saving=no
  ascript run /Applications/TextEdit.app open direct='"file.txt"'`,
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: completeCommands,
	RunE: func(cmd *cobra.Command, args []string) error {
		appPath := args[0]
		cmdName := args[1]
		appName := sdef.AppName(appPath)

		direct := ""
		params := map[string]string{}
		for _, kv := range args[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("invalid param %q: expected key=value", kv)
			}
			if k == "direct" {
				direct = v
			} else {
				params[k] = v
			}
		}

		script := sdef.BuildScript(appName, cmdName, direct, params)
		if jsonFlag(cmd) {
			fmt.Printf("{\"script\": %q}\n", script)
		}
		result, err := sdef.RunScript(script)
		if err != nil {
			return fmt.Errorf("osascript: %w", err)
		}
		fmt.Println(result)
		return nil
	},
}

// ── ascript script ────────────────────────────────────────────────────────────

var scriptCmd = &cobra.Command{
	Use:               "script <app-path> <command> [param=value...]",
	Short:             "Print the AppleScript that would be run (dry run)",
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: completeCommands,
	RunE: func(cmd *cobra.Command, args []string) error {
		appPath := args[0]
		cmdName := args[1]
		appName := sdef.AppName(appPath)

		direct := ""
		params := map[string]string{}
		for _, kv := range args[2:] {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				return fmt.Errorf("invalid param %q: expected key=value", kv)
			}
			if k == "direct" {
				direct = v
			} else {
				params[k] = v
			}
		}

		fmt.Println(sdef.BuildScript(appName, cmdName, direct, params))
		return nil
	},
}

// ── ascript get ───────────────────────────────────────────────────────────────

var getCmd = &cobra.Command{
	Use:   "get <app-path> <property> [object-expr]",
	Short: "Get a property of an application or one of its objects",
	Long: `Get a property from a scriptable application.

Examples:
  ascript get /Applications/Xcode.app name
  ascript get /Applications/Xcode.app version`,
	Args:              cobra.RangeArgs(2, 3),
	ValidArgsFunction: completeProperties,
	RunE: func(cmd *cobra.Command, args []string) error {
		appPath := args[0]
		propName := args[1]
		objectExpr := ""
		if len(args) > 2 {
			objectExpr = args[2]
		}
		appName := sdef.AppName(appPath)
		script := sdef.GetPropertyScript(appName, objectExpr, propName)
		result, err := sdef.RunScript(script)
		if err != nil {
			return fmt.Errorf("osascript: %w", err)
		}
		fmt.Println(result)
		return nil
	},
}
