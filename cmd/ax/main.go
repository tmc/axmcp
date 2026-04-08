// Command ax is a CLI for macOS Accessibility API navigation with dynamic tab completion.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/x/axuiautomation"
	"github.com/tmc/macgo"
	"github.com/tmc/axmcp/internal/ui"
)

func main() {
	runtime.LockOSThread()

	// Handle -h/--help before macgo.Start to avoid app bundle relaunch.
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" || arg == "-help" || arg == "help" {
			_ = rootCmd.Help()
			os.Exit(0)
		}
	}

	cfg := macgo.NewConfig().
		WithAppName("ax").
		WithPermissions(macgo.Accessibility).
		WithAdHocSign()
	cfg.BundleID = "dev.tmc.ax"

	if err := macgo.Start(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "macgo start failed: %v\n", err)
		os.Exit(1)
	}

	ui.CheckTrust()

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ax",
	Short: "macOS Accessibility API navigation",
	Long:  "ax navigates the macOS Accessibility tree of running applications.\nBundle IDs, roles, and titles complete dynamically via tab completion.",
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON")
	rootCmd.PersistentFlags().Int("depth", 4, "Max tree depth")
	rootCmd.PersistentFlags().Int("limit", 500, "Max elements to search")

	rootCmd.AddCommand(
		appsCmd,
		treeCmd,
		findCmd,
		clickCmd,
		typeCmd,
		attrCmd,
		menuCmd,
		focusCmd,
	)
}

// completeBundleIDs returns running app bundle IDs for tab completion.
func completeBundleIDs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out, err := exec.Command("lsappinfo", "list").Output()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var ids []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "bundleID=") {
			continue
		}
		id := strings.Trim(strings.TrimPrefix(line, "bundleID="), `"`)
		if id == "" || id == "[ NULL ]" {
			continue
		}
		if strings.HasPrefix(id, toComplete) {
			ids = append(ids, id)
		}
	}
	return ids, cobra.ShellCompDirectiveNoFileComp
}

// completeRoles returns AX role names for tab completion.
func completeRoles(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	roles := []string{
		"AXApplication", "AXWindow", "AXSheet", "AXDialog",
		"AXButton", "AXCheckBox", "AXRadioButton", "AXMenuItem", "AXMenuBarItem",
		"AXTextField", "AXTextArea", "AXSearchField", "AXStaticText",
		"AXTable", "AXOutline", "AXRow", "AXColumn", "AXCell",
		"AXScrollArea", "AXScrollBar", "AXSplitter", "AXTabGroup", "AXTab",
		"AXToolbar", "AXGroup", "AXList", "AXImage", "AXSlider",
		"AXProgressIndicator", "AXPopUpButton", "AXComboBox", "AXDisclosureTriangle",
		"AXMenu", "AXMenuButton", "AXLink", "AXWebArea",
	}
	var filtered []string
	for _, r := range roles {
		if strings.HasPrefix(strings.ToLower(r), strings.ToLower(toComplete)) {
			filtered = append(filtered, r)
		}
	}
	return filtered, cobra.ShellCompDirectiveNoFileComp
}

// completeTitles returns element titles from the given app for tab completion.
func completeTitles(bundleIDArgIndex int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) <= bundleIDArgIndex {
			return completeBundleIDs(cmd, args, toComplete)
		}
		app, err := openApp(args[bundleIDArgIndex])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer app.Close()

		limit, _ := cmd.Flags().GetInt("limit")
		seen := map[string]bool{}
		var titles []string
		app.Descendants().WithLimit(limit).ForEach(func(e *axuiautomation.Element) bool {
			t := e.Title()
			if t != "" && !seen[t] && strings.HasPrefix(strings.ToLower(t), strings.ToLower(toComplete)) {
				seen[t] = true
				titles = append(titles, t)
			}
			return true
		})
		return titles, cobra.ShellCompDirectiveNoFileComp
	}
}

// openApp opens an app by bundle ID or numeric PID string.
func openApp(arg string) (*axuiautomation.Application, error) {
	if pid, err := strconv.ParseInt(arg, 10, 32); err == nil {
		app := axuiautomation.NewApplicationFromPID(int32(pid))
		if app == nil {
			return nil, fmt.Errorf("cannot connect to PID %d", pid)
		}
		return app, nil
	}
	return axuiautomation.NewApplication(arg)
}

func jsonFlag(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

// --- commands ---

var appsCmd = &cobra.Command{
	Use:   "apps",
	Short: "List running apps with bundle IDs",
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.Command("lsappinfo", "list").Output()
		if err != nil {
			return err
		}
		type appInfo struct {
			Name     string `json:"name"`
			BundleID string `json:"bundle_id"`
			PID      int    `json:"pid"`
		}
		var apps []appInfo
		var cur appInfo
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, ") \"") && strings.Contains(line, "ASN:") {
				if cur.BundleID != "" {
					apps = append(apps, cur)
				}
				cur = appInfo{}
				// extract name between first pair of quotes
				s := line[strings.Index(line, "\"")+1:]
				cur.Name = s[:strings.Index(s, "\"")]
			} else if strings.HasPrefix(line, "bundleID=") {
				id := strings.Trim(strings.TrimPrefix(line, "bundleID="), `"`)
				if id != "[ NULL ]" {
					cur.BundleID = id
				}
			} else if strings.HasPrefix(line, "pid = ") {
				rest := strings.TrimPrefix(line, "pid = ")
				if i := strings.IndexAny(rest, " \t"); i > 0 {
					rest = rest[:i]
				}
				cur.PID, _ = strconv.Atoi(rest)
			}
		}
		if cur.BundleID != "" {
			apps = append(apps, cur)
		}
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(apps)
		}
		for _, a := range apps {
			fmt.Printf("%-45s  pid=%-6d  %s\n", a.BundleID, a.PID, a.Name)
		}
		return nil
	},
}

var treeCmd = &cobra.Command{
	Use:               "tree <bundle-id|pid>",
	Short:             "Print AX element tree",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBundleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()
		depth, _ := cmd.Flags().GetInt("depth")
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(elementToMap(app.Root(), depth))
		}
		printTree(app.Root(), 0, depth)
		return nil
	},
}

var findCmd = &cobra.Command{
	Use:               "find <bundle-id|pid>",
	Short:             "Find elements by role/title",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBundleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		role, _ := cmd.Flags().GetString("role")
		title, _ := cmd.Flags().GetString("title")
		contains, _ := cmd.Flags().GetString("contains")
		limit, _ := cmd.Flags().GetInt("limit")

		q := app.Descendants().WithLimit(limit)
		if role != "" {
			q = q.ByRole(role)
		}
		if title != "" {
			q = q.ByTitle(title)
		}
		if contains != "" {
			q = q.ByTitleContains(contains)
		}

		els := q.AllElements()
		if jsonFlag(cmd) {
			var out []map[string]any
			for _, e := range els {
				out = append(out, elementAttrs(e))
			}
			return json.NewEncoder(os.Stdout).Encode(out)
		}
		fmt.Printf("found %d elements\n", len(els))
		for i, e := range els {
			fmt.Printf("[%d] %s %q\n", i, e.Role(), e.Title())
		}
		return nil
	},
}

func init() {
	findCmd.Flags().StringP("role", "r", "", "Filter by AX role")
	findCmd.Flags().StringP("title", "t", "", "Filter by exact title")
	findCmd.Flags().StringP("contains", "c", "", "Filter by title substring")
	_ = findCmd.RegisterFlagCompletionFunc("role", completeRoles)
	_ = findCmd.RegisterFlagCompletionFunc("title", completeTitles(0))
	_ = findCmd.RegisterFlagCompletionFunc("contains", completeTitles(0))
}

var clickCmd = &cobra.Command{
	Use:               "click <bundle-id|pid> <title-contains>",
	Short:             "Click an element by title",
	Args:              cobra.ExactArgs(2),
	ValidArgsFunction: completeTitles(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()
		limit, _ := cmd.Flags().GetInt("limit")
		el := app.Descendants().WithLimit(limit).ByTitleContains(args[1]).First()
		if el == nil {
			return fmt.Errorf("element %q not found", args[1])
		}
		if err := el.Click(); err != nil {
			return err
		}
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"clicked": el.Title(), "role": el.Role()})
		}
		fmt.Printf("clicked %s %q\n", el.Role(), el.Title())
		return nil
	},
}

var typeCmd = &cobra.Command{
	Use:               "type <bundle-id|pid> <title-contains> <text>",
	Short:             "Type text into an element",
	Args:              cobra.ExactArgs(3),
	ValidArgsFunction: completeTitles(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()
		limit, _ := cmd.Flags().GetInt("limit")
		el := app.Descendants().WithLimit(limit).ByTitleContains(args[1]).First()
		if el == nil {
			// also try search fields / text fields
			el = app.Descendants().WithLimit(limit).ByRole("AXTextField").ByTitleContains(args[1]).First()
		}
		if el == nil {
			return fmt.Errorf("element %q not found", args[1])
		}
		if err := el.Click(); err != nil {
			return fmt.Errorf("focus: %w", err)
		}
		if err := el.TypeText(args[2]); err != nil {
			return err
		}
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"typed": args[2], "into": el.Title()})
		}
		fmt.Printf("typed into %s %q\n", el.Role(), el.Title())
		return nil
	},
}

var attrNames = []string{"AXRole", "AXTitle", "AXValue", "AXSubrole"}

var attrCmd = &cobra.Command{
	Use:   "attr <bundle-id|pid> <attribute>",
	Short: "Get an attribute of the focused element or root",
	Args:  cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return completeBundleIDs(cmd, args, toComplete)
		}
		var out []string
		for _, a := range attrNames {
			if strings.HasPrefix(a, toComplete) {
				out = append(out, a)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()

		el := app.FocusedElement()
		if el == nil {
			el = app.Root()
		}
		val := el.Role() // default
		switch args[1] {
		case "AXRole":
			val = el.Role()
		case "AXTitle":
			val = el.Title()
		case "AXValue":
			val = el.Value()
		case "AXSubrole":
			val = el.Subrole()
		default:
			return fmt.Errorf("unknown attribute %q; supported: AXRole AXTitle AXValue AXSubrole", args[1])
		}
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{args[1]: val})
		}
		fmt.Println(val)
		return nil
	},
}

var menuCmd = &cobra.Command{
	Use:               "menu <bundle-id|pid> <item> [subitem...]",
	Short:             "Click a menu item by path",
	Args:              cobra.MinimumNArgs(2),
	ValidArgsFunction: completeBundleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()
		path := args[1:]
		if err := app.ClickMenuItem(path); err != nil {
			return err
		}
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"clicked_menu": path})
		}
		fmt.Printf("clicked menu: %s\n", strings.Join(path, " > "))
		return nil
	},
}

var focusCmd = &cobra.Command{
	Use:               "focus <bundle-id|pid>",
	Short:             "Show the currently focused element",
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: completeBundleIDs,
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		defer app.Close()
		el := app.FocusedElement()
		if el == nil {
			return fmt.Errorf("no focused element")
		}
		if jsonFlag(cmd) {
			return json.NewEncoder(os.Stdout).Encode(elementAttrs(el))
		}
		fmt.Printf("role=%s title=%q value=%q\n", el.Role(), el.Title(), el.Value())
		return nil
	},
}

// --- helpers ---

func printTree(el *axuiautomation.Element, indent, maxDepth int) {
	if el == nil || indent > maxDepth {
		return
	}
	prefix := strings.Repeat("  ", indent)
	role := el.Role()
	var parts []string
	if t := el.Title(); t != "" {
		parts = append(parts, fmt.Sprintf("title=%q", t))
	}
	if v := el.Value(); v != "" && v != "0" {
		parts = append(parts, fmt.Sprintf("value=%q", v))
	}
	if len(parts) > 0 {
		fmt.Printf("%s%s [%s]\n", prefix, role, strings.Join(parts, " "))
	} else {
		fmt.Printf("%s%s\n", prefix, role)
	}
	for _, c := range el.Children() {
		printTree(c, indent+1, maxDepth)
	}
}

func elementAttrs(e *axuiautomation.Element) map[string]any {
	x, y := e.Position()
	w, h := e.Size()
	return map[string]any{
		"role": e.Role(), "title": e.Title(), "value": e.Value(),
		"subrole": e.Subrole(), "enabled": e.IsEnabled(),
		"x": x, "y": y, "w": w, "h": h,
	}
}

func elementToMap(e *axuiautomation.Element, depth int) map[string]any {
	if e == nil {
		return nil
	}
	m := elementAttrs(e)
	if depth > 0 {
		var children []map[string]any
		for _, c := range e.Children() {
			children = append(children, elementToMap(c, depth-1))
		}
		if len(children) > 0 {
			m["children"] = children
		}
	}
	return m
}
