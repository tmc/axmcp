//go:build darwin

package main

// AX pipeline mini-language
//
// Syntax: ax pipe <stage> [args] // <stage> [args] // ...
//
// Stages are separated by "//" to avoid conflict with shell pipes.
//
// Stages that produce a context:
//   app <bundle-id|pid>          → *Application
//   window [title-contains]      → *Element (first matching window)
//   windows                      → []*Element
//   find [--role R] [--title T]  → []*Element
//   children                     → []*Element
//   focus                        → *Element
//   first                        → *Element (first of list)
//
// Action stages (terminate the pipeline):
//   tree [--depth N]             → print tree
//   click                        → click focused element
//   type <text>                  → type text into focused element
//   attr <name>                  → print attribute
//   click-menu <A->B->C>         → click menu path (arrow-separated)
//   list                         → print elements
//   json                         → print as JSON
//
// Example:
//   ax pipe app com.apple.dt.Xcode // window // click-menu 'File->New->Target...'
//   ax pipe app Xcode // find --role AXButton // list
//   ax pipe app Xcode // focus // attr AXTitle

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tmc/apple/x/axuiautomation"
)

// pipeContext holds the current state as the pipeline executes.
type pipeContext struct {
	app      *axuiautomation.Application
	element  *axuiautomation.Element   // single element focus
	elements []*axuiautomation.Element // list focus
}

func (pc *pipeContext) close() {
	if pc.app != nil {
		pc.app.Close()
	}
}

// stagesAfterContext returns the valid next stages given the current context type.
func stagesAfterContext(pc *pipeContext) []string {
	if pc == nil || pc.app == nil {
		return []string{"app"}
	}
	base := []string{".", "window", "windows", "find", "focus", "tree", "json", "click-menu"}
	if pc.element != nil {
		base = append(base, "click", "type", "attr", "children", "list", "json")
	}
	if len(pc.elements) > 0 {
		base = append(base, "first", "list", "json", "click")
	}
	return base
}

// terminalStages are pipeline stages that produce output (rather than context).
var terminalStages = map[string]bool{
	".": true, "tree": true, "list": true, "json": true,
	"click": true, "type": true, "attr": true, "click-menu": true,
}

// parseAndExecutePipeline parses and runs the pipeline string.
// If the last stage is navigational (not terminal), it prints a default
// text representation of whatever the pipeline resolved to.
func parseAndExecutePipeline(expr string) error {
	stages := splitPipelineExec(expr)
	if len(stages) == 0 {
		return fmt.Errorf("empty pipeline")
	}

	pc := &pipeContext{}
	defer pc.close()

	// Spin the run loop once so AX IPC replies are delivered in this CLI process.
	axuiautomation.SpinRunLoop(200 * time.Millisecond)

	var lastCmd string
	for _, stage := range stages {
		parts := tokenize(stage)
		if len(parts) == 0 {
			continue
		}
		lastCmd = parts[0]
		if err := execStage(pc, parts); err != nil {
			return fmt.Errorf("stage %q: %w", stage, err)
		}
	}

	if !terminalStages[lastCmd] {
		printContext(pc)
	}
	return nil
}

// printContext prints a human-readable summary of the current pipeline context.
func printContext(pc *pipeContext) {
	switch {
	case len(pc.elements) > 0:
		for i, e := range pc.elements {
			printElement(i, e)
		}
	case pc.element != nil:
		printElement(-1, pc.element)
	case pc.app != nil:
		fmt.Printf("app pid=%d bundle=%q\n", pc.app.PID(), pc.app.BundleID())
	}
}

func printElement(idx int, e *axuiautomation.Element) {
	role := e.Role()
	title := e.Title()
	val := e.Value()
	var parts []string
	if idx >= 0 {
		parts = append(parts, fmt.Sprintf("[%d]", idx))
	}
	parts = append(parts, role)
	if title != "" {
		parts = append(parts, fmt.Sprintf("%q", title))
	}
	if val != "" && val != title {
		parts = append(parts, fmt.Sprintf("= %q", val))
	}
	fmt.Println(strings.Join(parts, " "))
}

// splitPipelineExec splits on | and trims all stages (for execution).
func splitPipelineExec(s string) []string {
	stages := splitPipeline(s)
	for i, st := range stages {
		stages[i] = strings.TrimSpace(st)
	}
	return stages
}

// splitPipeline splits on "//" separators but not inside quotes.
// The last stage is NOT trimmed so completion can detect trailing spaces.
func splitPipeline(s string) []string {
	var stages []string
	var cur strings.Builder
	inQ := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		ch := runes[i]
		switch {
		case ch == '\'' || ch == '"':
			inQ = !inQ
			cur.WriteRune(ch)
		case ch == '/' && !inQ && i+1 < len(runes) && runes[i+1] == '/':
			stages = append(stages, strings.TrimSpace(cur.String()))
			cur.Reset()
			i++ // skip second /
		default:
			cur.WriteRune(ch)
		}
	}
	// Keep the last stage as-is (don't TrimSpace) so callers can detect
	// whether the user typed a trailing space (meaning: next arg expected).
	if last := cur.String(); strings.TrimSpace(last) != "" || strings.HasSuffix(last, " ") {
		stages = append(stages, last)
	}
	return stages
}

// tokenize splits a stage into tokens, respecting quotes.
func tokenize(s string) []string {
	var tokens []string
	var cur strings.Builder
	inQ := rune(0)
	for _, ch := range s {
		switch {
		case inQ != 0 && ch == inQ:
			inQ = 0
		case inQ == 0 && (ch == '\'' || ch == '"'):
			inQ = ch
		case inQ == 0 && ch == ' ':
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(ch)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func execStage(pc *pipeContext, parts []string) error {
	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "app":
		if len(args) == 0 {
			return fmt.Errorf("app: requires bundle-id or pid")
		}
		app, err := openApp(args[0])
		if err != nil {
			return err
		}
		if pc.app != nil {
			pc.app.Close()
		}
		pc.app = app
		pc.element = app.Root()
		pc.elements = nil

	case "window":
		if pc.app == nil {
			return fmt.Errorf("window: no app in context")
		}
		wins := pc.app.WindowList()
		if len(args) > 0 {
			substr := strings.ToLower(args[0])
			for _, w := range wins {
				if strings.Contains(strings.ToLower(w.Title()), substr) {
					pc.element = w
					break
				}
			}
		} else if len(wins) > 0 {
			pc.element = wins[0]
		}
		pc.elements = nil
		if pc.element == nil {
			return fmt.Errorf("window: not found")
		}

	case "windows":
		if pc.app == nil {
			return fmt.Errorf("windows: no app in context")
		}
		pc.elements = pc.app.WindowList()
		pc.element = nil

	case "focus":
		if pc.app == nil {
			return fmt.Errorf("focus: no app in context")
		}
		pc.element = pc.app.FocusedElement()
		pc.elements = nil
		if pc.element == nil {
			return fmt.Errorf("focus: no focused element")
		}

	case "children":
		if pc.element == nil {
			return fmt.Errorf("children: no element in context")
		}
		pc.elements = pc.element.Children()
		pc.element = nil

	case "first":
		if len(pc.elements) == 0 {
			return fmt.Errorf("first: no elements in context")
		}
		pc.element = pc.elements[0]
		pc.elements = nil

	case "find":
		root := pc.element
		if root == nil && pc.app != nil {
			root = pc.app.Root()
		}
		if root == nil {
			return fmt.Errorf("find: no context")
		}
		q := root.Descendants()
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--role", "-r":
				if i+1 < len(args) {
					q = q.ByRole(args[i+1])
					i++
				}
			case "--title", "-t":
				if i+1 < len(args) {
					q = q.ByTitle(args[i+1])
					i++
				}
			case "--contains", "-c":
				if i+1 < len(args) {
					q = q.ByTitleContains(args[i+1])
					i++
				}
			}
		}
		pc.elements = q.AllElements()
		pc.element = nil

	case "click":
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("click: no element in context")
		}
		if err := el.Click(); err != nil {
			return err
		}
		fmt.Printf("clicked %s %q\n", el.Role(), el.Title())

	case "type":
		if len(args) == 0 {
			return fmt.Errorf("type: requires text argument")
		}
		el := pc.element
		if el == nil {
			return fmt.Errorf("type: no element in context")
		}
		if err := el.TypeText(strings.Join(args, " ")); err != nil {
			return err
		}
		fmt.Printf("typed %q into %s\n", strings.Join(args, " "), el.Role())

	case "attr":
		if len(args) == 0 {
			return fmt.Errorf("attr: requires attribute name")
		}
		el := pc.element
		if el == nil {
			return fmt.Errorf("attr: no element in context")
		}
		var val string
		switch args[0] {
		case "AXRole":
			val = el.Role()
		case "AXTitle":
			val = el.Title()
		case "AXValue":
			val = el.Value()
		case "AXSubrole":
			val = el.Subrole()
		default:
			return fmt.Errorf("attr: unknown attribute %q", args[0])
		}
		fmt.Println(val)

	case "click-menu":
		if pc.app == nil {
			return fmt.Errorf("click-menu: no app in context")
		}
		if len(args) == 0 {
			return fmt.Errorf("click-menu: requires menu path (e.g. 'File->New->Target')")
		}
		// Support both -> and space-separated args as path segments.
		var path []string
		if strings.Contains(args[0], "->") {
			path = strings.Split(args[0], "->")
		} else {
			path = args
		}
		if err := pc.app.ClickMenuItem(path); err != nil {
			return err
		}
		fmt.Printf("clicked menu: %s\n", strings.Join(path, " > "))

	case "tree":
		root := pc.element
		if root == nil && pc.app != nil {
			root = pc.app.Root()
		}
		if root == nil {
			return fmt.Errorf("tree: no element in context")
		}
		depth := 4
		for i := 0; i < len(args); i++ {
			if args[i] == "--depth" || args[i] == "-d" {
				if i+1 < len(args) {
					if d, err := strconv.Atoi(args[i+1]); err == nil {
						depth = d
					}
					i++
				}
			}
		}
		printTree(root, 0, depth)

	case ".":
		printContext(pc)

	case "list":
		els := pc.elements
		if els == nil && pc.element != nil {
			els = []*axuiautomation.Element{pc.element}
		}
		for i, e := range els {
			fmt.Printf("[%d] %s %q\n", i, e.Role(), e.Title())
		}

	case "json":
		var out any
		if pc.elements != nil {
			var arr []map[string]any
			for _, e := range pc.elements {
				arr = append(arr, elementAttrs(e))
			}
			out = arr
		} else if pc.element != nil {
			out = elementAttrs(pc.element)
		} else {
			out = map[string]any{}
		}
		return json.NewEncoder(os.Stdout).Encode(out)

	default:
		return fmt.Errorf("unknown stage %q", cmd)
	}
	return nil
}

// completePipeline provides shell completion for the pipeline expression.
// cobra passes already-completed words in args and the word being typed in
// toComplete, so we reconstruct the full expression and delegate to the
// single-string completer.
func completePipeline(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completePipelineWords(args, toComplete)
}

// completePipelineWords is the real completion engine, called with cobra's
// args (already-complete words) and toComplete (current word being typed).
func completePipelineWords(args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Reconstruct everything typed so far (completed args + partial word).
	allWords := append(args, toComplete)

	// Split into pipeline stages. The last stage contains the partial word.
	// We split on "//" tokens in allWords.
	var stages [][]string // each stage is a slice of words
	cur := []string{}
	for _, w := range allWords {
		if w == "//" {
			stages = append(stages, cur)
			cur = []string{}
		} else {
			cur = append(cur, w)
		}
	}
	stages = append(stages, cur)

	// Execute all but the last stage to build context.
	pc := &pipeContext{}
	defer pc.close()
	for _, stage := range stages[:len(stages)-1] {
		if len(stage) == 0 {
			continue
		}
		_ = execStage(pc, stage)
	}

	lastStage := stages[len(stages)-1]

	// Determine what we're completing.
	// lastStage[0..n-1] are committed words in this stage; toComplete is the partial.

	// If the last stage is empty or only has "|" context, suggest stage names.
	if len(lastStage) == 0 || (len(lastStage) == 1 && toComplete == lastStage[0]) {
		// Completing the stage name.
		partial := ""
		if len(lastStage) == 1 {
			partial = lastStage[0]
		}
		var out []string
		for _, s := range stagesAfterContext(pc) {
			if strings.HasPrefix(s, partial) {
				out = append(out, s)
			}
		}
		return out, cobra.ShellCompDirectiveNoSpace | cobra.ShellCompDirectiveNoFileComp
	}

	stageName := lastStage[0]
	// stageArgs are the committed args within this stage (not including toComplete).
	stageArgs := lastStage[1:]
	if len(stageArgs) > 0 && stageArgs[len(stageArgs)-1] == toComplete {
		stageArgs = stageArgs[:len(stageArgs)-1]
	}

	switch stageName {
	case "app":
		// If the app argument is already provided, suggest // separator.
		if len(stageArgs) >= 1 && strings.HasPrefix("//", toComplete) {
			return []string{"//"}, cobra.ShellCompDirectiveNoFileComp
		}
		ids, _ := completeBundleIDs(nil, nil, toComplete)
		return ids, cobra.ShellCompDirectiveNoFileComp

	case "window":
		// If already has an arg, suggest // separator.
		if len(stageArgs) >= 1 && strings.HasPrefix("//", toComplete) {
			return []string{"//"}, cobra.ShellCompDirectiveNoFileComp
		}
		if pc.app != nil {
			var out []string
			for _, w := range pc.app.Windows().AllElements() {
				t := w.Title()
				if t != "" && strings.HasPrefix(strings.ToLower(t), strings.ToLower(toComplete)) {
					out = append(out, t)
				}
			}
			return out, cobra.ShellCompDirectiveNoFileComp
		}

	case "find":
		// If previous arg was --role, complete role names.
		if len(stageArgs) > 0 && (stageArgs[len(stageArgs)-1] == "--role" || stageArgs[len(stageArgs)-1] == "-r") {
			return completeRoles(nil, nil, toComplete)
		}
		// Otherwise suggest flags.
		flags := []string{"--role", "--title", "--contains"}
		var out []string
		for _, f := range flags {
			if strings.HasPrefix(f, toComplete) {
				out = append(out, f)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp

	case "attr":
		var out []string
		for _, a := range attrNames {
			if strings.HasPrefix(a, toComplete) {
				out = append(out, a)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp

	case "click-menu":
		if pc.app != nil {
			menuBar := pc.app.MenuBar()
			if menuBar != nil {
				arrowIdx := strings.LastIndex(toComplete, "->")
				prefix, partial := "", toComplete
				if arrowIdx >= 0 {
					prefix = toComplete[:arrowIdx+2]
					partial = toComplete[arrowIdx+2:]
				}
				var out []string
				for _, child := range menuBar.Children() {
					t := child.Title()
					if t != "" && strings.HasPrefix(strings.ToLower(t), strings.ToLower(partial)) {
						out = append(out, prefix+t)
					}
				}
				return out, cobra.ShellCompDirectiveNoFileComp
			}
		}

	case ".", "tree", "list", "json", "click", "focus", "children", "windows", "first":
		// These stages take no args; suggest // to add the next stage.
		if strings.HasPrefix("//", toComplete) {
			return []string{"//"}, cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return nil, cobra.ShellCompDirectiveNoFileComp
}

var pipeCmd = &cobra.Command{
	Use:   "pipe <stage> [args] [// <stage> [args]]...",
	Short: "Run a pipeline expression (e.g. app Xcode // window // list)",
	Long: `Execute a pipeline of AX navigation stages separated by //.

Stages:
  app <bundle-id|pid>              open an application
  window [title-contains]          focus a window
  windows                          list all windows
  focus                            get focused element
  children                         get children of current element
  first                            take first element from list
  find [--role R] [--title T]      search descendants

Actions (terminate pipeline):
  tree [--depth N]                 print element tree
  click                            click current element
  type <text>                      type text into current element
  attr <AXAttr>                    print attribute value
  click-menu <A->B->C>             click menu path
  list                             print element list
  json                             print as JSON

Examples:
  ax pipe app com.apple.dt.Xcode // window // click-menu 'File->New->Target...'
  ax pipe app Xcode // find --role AXButton // list
  ax pipe app Finder // focus // attr AXTitle`,
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completePipeline,
	RunE: func(cmd *cobra.Command, args []string) error {
		return parseAndExecutePipeline(strings.Join(args, " "))
	},
}

func init() {
	rootCmd.AddCommand(pipeCmd)
}
