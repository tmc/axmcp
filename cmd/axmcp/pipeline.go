package main

// AX pipeline execution for axmcp.
//
// This mirrors cmd/ax/pipe.go but writes output to a strings.Builder
// so the MCP tool can return it as a string rather than printing to stdout.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/xcmcp/axuiautomation"
)

// pipeContext holds the current state as the pipeline executes.
type pipeContext struct {
	app      *axuiautomation.Application
	element  *axuiautomation.Element
	elements []*axuiautomation.Element
}

func (pc *pipeContext) close() {
	if pc.app != nil {
		pc.app.Close()
	}
}

// execPipeline runs the pipeline string and returns the output as a string.
func execPipeline(expr string) (string, error) {
	stages := splitPipelineExec(expr)
	if len(stages) == 0 {
		return "", fmt.Errorf("empty pipeline")
	}

	pc := &pipeContext{}
	defer pc.close()

	axuiautomation.SpinRunLoop(200 * time.Millisecond)

	var buf strings.Builder
	var lastCmd string
	for _, stage := range stages {
		parts := tokenize(stage)
		if len(parts) == 0 {
			continue
		}
		lastCmd = parts[0]
		if err := execStageWriter(pc, parts, &buf); err != nil {
			return buf.String(), fmt.Errorf("stage %q: %w", stage, err)
		}
	}

	if !terminalStage[lastCmd] {
		writeContext(&buf, pc)
	}
	return buf.String(), nil
}

// terminalStage marks stages that produce their own output.
var terminalStage = map[string]bool{
	".": true, "tree": true, "list": true, "json": true,
	"click": true, "type": true, "attr": true, "click-menu": true,
}

// splitPipelineExec splits the pipeline string on // separators and trims stages.
func splitPipelineExec(s string) []string {
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
			if st := strings.TrimSpace(cur.String()); st != "" {
				stages = append(stages, st)
			}
			cur.Reset()
			i++
		default:
			cur.WriteRune(ch)
		}
	}
	if st := strings.TrimSpace(cur.String()); st != "" {
		stages = append(stages, st)
	}
	return stages
}

// tokenize splits a stage string into tokens, respecting single/double quotes.
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

func writeContext(buf *strings.Builder, pc *pipeContext) {
	switch {
	case len(pc.elements) > 0:
		for i, e := range pc.elements {
			writeElement(buf, i, e)
		}
	case pc.element != nil:
		writeElement(buf, -1, pc.element)
	case pc.app != nil:
		fmt.Fprintf(buf, "app pid=%d bundle=%q\n", pc.app.PID(), pc.app.BundleID())
	}
}

func writeElement(buf *strings.Builder, idx int, e *axuiautomation.Element) {
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
	fmt.Fprintln(buf, strings.Join(parts, " "))
}

func writeTree(buf *strings.Builder, e *axuiautomation.Element, indent, maxDepth int) {
	if e == nil || indent > maxDepth {
		return
	}
	fmt.Fprintf(buf, "%s%s\n", strings.Repeat("  ", indent), elementSummary(e))
	for _, c := range e.Children() {
		writeTree(buf, c, indent+1, maxDepth)
	}
}

func execStageWriter(pc *pipeContext, parts []string, buf *strings.Builder) error {
	cmd := parts[0]
	args := parts[1:]

	switch cmd {
	case "apps":
		out, err := exec.Command("lsappinfo", "list").Output()
		if err != nil {
			return fmt.Errorf("lsappinfo failed: %w", err)
		}
		// Assuming we want a human readable string since stages return text.
		buf.Write(parseAppsTable(out))

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
		// Spin again after opening the app so AX IPC is primed for this specific app.
		axuiautomation.SpinRunLoop(200 * time.Millisecond)

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
		if pc.app != nil {
			pc.elements = pc.app.WindowList()
			pc.element = nil
		} else {
			// Global windows: find all PIDs from lsappinfo and get their windows.
			out, err := exec.Command("lsappinfo", "list").Output()
			if err != nil {
				return fmt.Errorf("lsappinfo failed: %w", err)
			}
			var allWins []*axuiautomation.Element
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "pid = ") {
					rest := strings.TrimPrefix(line, "pid = ")
					if i := strings.IndexAny(rest, " \t"); i > 0 {
						rest = rest[:i]
					}
					if pid, err := strconv.ParseInt(rest, 10, 32); err == nil {
						app := axuiautomation.NewApplicationFromPID(int32(pid))
						if app != nil {
							// We can't defer app.Close() here because Elements refer to app,
							// but for a quick script it's fine.
							allWins = append(allWins, app.WindowList()...)
						}
					}
				}
			}
			pc.elements = allWins
			pc.element = nil
		}

	case "focus":
		if pc.app == nil {
			return fmt.Errorf("focus: no app in context")
		}
		pc.element = pc.app.FocusedElement()
		pc.elements = nil
		if pc.element == nil {
			return fmt.Errorf("focus: no focused element")
		}

	case "raise":
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("raise: no element in context")
		}
		if err := el.Raise(); err != nil {
			return err
		}
		fmt.Fprintf(buf, "raised %s %q\n", el.Role(), el.Title())

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
			case "--id", "-i":
				if i+1 < len(args) {
					q = q.ByIdentifier(args[i+1])
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
		fmt.Fprintf(buf, "clicked %s %q\n", el.Role(), el.Title())

	case "click-at":
		if len(args) != 2 {
			return fmt.Errorf("click-at: requires x and y offset arguments")
		}
		xOffset, errX := strconv.Atoi(args[0])
		yOffset, errY := strconv.Atoi(args[1])
		if errX != nil || errY != nil {
			return fmt.Errorf("click-at: offsets must be integers")
		}
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("click-at: no element in context")
		}
		if err := el.ClickAt(xOffset, yOffset); err != nil {
			return err
		}
		fmt.Fprintf(buf, "clicked-at %d,%d %s %q\n", xOffset, yOffset, el.Role(), el.Title())

	case "hover":
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("hover: no element in context")
		}
		if err := el.Hover(); err != nil {
			return err
		}
		fmt.Fprintf(buf, "hovered %s %q\n", el.Role(), el.Title())

	case "type":
		if len(args) == 0 {
			return fmt.Errorf("type: requires text argument")
		}
		el := pc.element
		if el == nil {
			return fmt.Errorf("type: no element in context")
		}
		text := strings.Join(args, " ")
		if err := el.TypeText(text); err != nil {
			return err
		}
		fmt.Fprintf(buf, "typed %q into %s\n", text, el.Role())

	case "attr":
		if len(args) == 0 {
			return fmt.Errorf("attr: requires attribute name")
		}
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
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
		fmt.Fprintln(buf, val)

	case "click-menu":
		if pc.app == nil {
			return fmt.Errorf("click-menu: no app in context")
		}
		if len(args) == 0 {
			return fmt.Errorf("click-menu: requires menu path (e.g. 'File->New->Target')")
		}
		var path []string
		if strings.Contains(args[0], "->") {
			path = strings.Split(args[0], "->")
		} else {
			path = args
		}
		if err := pc.app.ClickMenuItem(path); err != nil {
			return err
		}
		fmt.Fprintf(buf, "clicked menu: %s\n", strings.Join(path, " > "))

	case "tree":
		root := pc.element
		if root == nil && len(pc.elements) > 0 {
			root = pc.elements[0]
		}
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
		writeTree(buf, root, 0, depth)

	case ".":
		writeContext(buf, pc)

	case "list":
		els := pc.elements
		if els == nil && pc.element != nil {
			els = []*axuiautomation.Element{pc.element}
		}
		for i, e := range els {
			fmt.Fprintf(buf, "[%d] %s %q\n", i, e.Role(), e.Title())
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
		enc := json.NewEncoder(buf)
		enc.SetIndent("", "  ")
		return enc.Encode(out)

	default:
		return fmt.Errorf("unknown stage %q. Available stages:\n"+
			"  apps\n"+
			"  app <bundle-id|pid>\n"+
			"  window [substr]\n"+
			"  windows\n"+
			"  focus\n"+
			"  raise\n"+
			"  children\n"+
			"  first\n"+
			"  find [--role R] [--title T] [--contains C] [--id I]\n"+
			"  .\n"+
			"  tree [--depth N]\n"+
			"  list\n"+
			"  json\n"+
			"  click\n"+
			"  click-at <x> <y>\n"+
			"  hover\n"+
			"  type <text>\n"+
			"  attr <AXAttr>\n"+
			"  click-menu <A> <B> <C>", cmd)
	}
	return nil
}
