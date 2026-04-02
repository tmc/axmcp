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

	"github.com/tmc/apple/x/axuiautomation"
)

// pipeContext holds the current state as the pipeline executes.
type pipeContext struct {
	app      *axuiautomation.Application
	element  *axuiautomation.Element
	elements []*axuiautomation.Element
	findNote string
	findPick *matchedElement
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
	"click": true, "rightclick": true, "hover": true, "type": true, "attr": true, "click-menu": true,
	"ocr": true, "ocr-hover": true, "highlight": true, "action": true,
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
	if pc.findNote != "" {
		fmt.Fprintln(buf, pc.findNote)
	}
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
	var parts []string
	if idx >= 0 {
		parts = append(parts, fmt.Sprintf("[%d]", idx))
	}
	parts = append(parts, formatSnapshot(snapshotElement(e, 0, idx)))
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

func capturePipelineOCRScope(pc *pipeContext) (*ocrCapture, error) {
	if pc == nil {
		return nil, fmt.Errorf("ocr: no pipeline context")
	}
	capture := &ocrCapture{}
	if pc.element != nil {
		if results, w, h, err := ocrElementWithSize(pc.element); err == nil {
			capture.target = pc.element
			capture.desc = formatSnapshot(snapshotElement(pc.element, 0, 0))
			capture.imgW = w
			capture.imgH = h
			capture.result = results
			return capture, nil
		}
	}
	if pc.app == nil {
		return nil, fmt.Errorf("ocr: no element or app in context")
	}
	target := pc.app.MainWindow()
	if target == nil {
		windows := pc.app.WindowList()
		if len(windows) > 0 {
			target = windows[0]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("ocr: no window in context")
	}
	if results, w, h, err := ocrElementWithSize(target); err == nil {
		capture.target = target
		capture.desc = formatSnapshot(snapshotElement(target, 0, 0))
		capture.imgW = w
		capture.imgH = h
		capture.result = results
		return capture, nil
	}

	title := target.Title()
	var appIDs []string
	if pid := pc.app.PID(); pid > 0 {
		appIDs = append(appIDs, strconv.Itoa(int(pid)))
	}
	if bundleID := strings.TrimSpace(pc.app.BundleID()); bundleID != "" {
		appIDs = append(appIDs, bundleID)
	}
	if root := pc.app.Root(); root != nil {
		if name := strings.TrimSpace(root.Title()); name != "" {
			appIDs = append(appIDs, name)
		}
	}
	for _, appID := range appIDs {
		if results, w, h, err := ocrWindow(appID, title); err == nil {
			capture.target = target
			capture.desc = formatSnapshot(snapshotElement(target, 0, 0))
			capture.imgW = w
			capture.imgH = h
			capture.result = results
			return capture, nil
		}
	}
	return nil, fmt.Errorf("ocr: could not capture current scope")
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
		pc.findNote = ""
		pc.findPick = nil
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
		pc.findNote = ""
		pc.findPick = nil
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
		pc.findNote = ""
		pc.findPick = nil

	case "focus":
		if pc.app == nil {
			return fmt.Errorf("focus: no app in context")
		}
		pc.element = pc.app.FocusedElement()
		pc.elements = nil
		pc.findNote = ""
		pc.findPick = nil
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
		pc.findNote = ""
		pc.findPick = nil

	case "children":
		if pc.element == nil {
			return fmt.Errorf("children: no element in context")
		}
		pc.elements = pc.element.Children()
		pc.element = nil
		pc.findNote = ""
		pc.findPick = nil

	case "first":
		if len(pc.elements) == 0 {
			return fmt.Errorf("first: no elements in context")
		}
		pc.element = pc.elements[0]
		pc.elements = nil
		pc.findNote = ""
		pc.findPick = nil

	case "find":
		root := pc.element
		if root == nil && pc.app != nil {
			root = pc.app.Root()
		}
		if root == nil {
			return fmt.Errorf("find: no context")
		}
		opts := searchOptions{Limit: 200}
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--role", "-r":
				if i+1 < len(args) {
					opts.Role = args[i+1]
					i++
				}
			case "--title", "-t":
				if i+1 < len(args) {
					opts.Title = args[i+1]
					i++
				}
			case "--contains", "-c":
				if i+1 < len(args) {
					opts.Contains = args[i+1]
					i++
				}
			case "--id", "-i":
				if i+1 < len(args) {
					opts.Identifier = args[i+1]
					i++
				}
			}
		}
		result := findElements(root, opts)
		if len(result.matches) == 0 {
			return fmt.Errorf("%s", noMatchMessage(result))
		}
		pc.elements = make([]*axuiautomation.Element, 0, len(result.matches))
		for _, match := range result.matches {
			pc.elements = append(pc.elements, match.snapshot.element)
		}
		pc.element = nil
		pc.findNote = selectionReason(result)
		pc.findPick = &result.matches[0]

	case "click":
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("click: no element in context")
		}
		target := el
		var clickNote string
		if pc.findPick != nil && target == pc.findPick.snapshot.element {
			resolution := resolveClickTarget(*pc.findPick, 50)
			clickNote = resolution.reason
			if resolution.target.element != nil {
				target = resolution.target.element
			}
		}
		clickSummary, err := performDefaultClick(snapshotElement(target, 0, 0))
		if err != nil {
			return err
		}
		fmt.Fprintln(buf, clickSummary)
		if pc.findNote != "" {
			fmt.Fprintln(buf, pc.findNote)
		}
		if clickNote != "" {
			fmt.Fprintln(buf, clickNote)
		}

	case "rightclick":
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("rightclick: no element in context")
		}
		target := el
		var clickNote string
		if pc.findPick != nil && target == pc.findPick.snapshot.element {
			resolution := resolveClickTarget(*pc.findPick, 50)
			clickNote = resolution.reason
			if resolution.target.element != nil {
				target = resolution.target.element
			}
		}
		clickSummary, err := performDefaultRightClick(snapshotElement(target, 0, 0))
		if err != nil {
			return err
		}
		fmt.Fprintln(buf, clickSummary)
		if pc.findNote != "" {
			fmt.Fprintln(buf, pc.findNote)
		}
		if clickNote != "" {
			fmt.Fprintln(buf, clickNote)
		}

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
		target := el
		var clickNote string
		if pc.findPick != nil && target == pc.findPick.snapshot.element {
			resolution := resolveClickTarget(*pc.findPick, 50)
			clickNote = resolution.reason
			if resolution.target.element != nil {
				target = resolution.target.element
			}
		}
		if err := clickLocalPoint(target, xOffset, yOffset); err != nil {
			return err
		}
		fmt.Fprintf(buf, "clicked-at %d,%d %s\n", xOffset, yOffset, formatSnapshot(snapshotElement(target, 0, 0)))
		if pc.findNote != "" {
			fmt.Fprintln(buf, pc.findNote)
		}
		if clickNote != "" {
			fmt.Fprintln(buf, clickNote)
		}

	case "hover":
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("hover: no element in context")
		}
		target := el
		var hoverNote string
		if pc.findPick != nil && target == pc.findPick.snapshot.element {
			resolution := resolveClickTarget(*pc.findPick, 50)
			hoverNote = resolution.reason
			if resolution.target.element != nil {
				target = resolution.target.element
			}
		}
		hoverSummary, err := performDefaultHover(snapshotElement(target, 0, 0))
		if err != nil {
			return err
		}
		fmt.Fprintln(buf, hoverSummary)
		if pc.findNote != "" {
			fmt.Fprintln(buf, pc.findNote)
		}
		if hoverNote != "" {
			fmt.Fprintln(buf, hoverNote)
		}

	case "type":
		if len(args) == 0 {
			return fmt.Errorf("type: requires text argument")
		}
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("type: no element in context")
		}
		text := strings.Join(args, " ")
		// For text fields, prefer SetValue to avoid cursor warp from CGEvent click.
		role := el.Role()
		if role == "AXTextField" || role == "AXTextArea" || role == "AXComboBox" {
			if err := el.SetValue(text); err == nil {
				fmt.Fprintf(buf, "set value %q on %s\n", text, formatSnapshot(snapshotElement(el, 0, 0)))
				break
			}
			// Fall through to TypeText if SetValue fails.
		}
		if err := el.TypeText(text); err != nil {
			return err
		}
		fmt.Fprintf(buf, "typed %q into %s\n", text, formatSnapshot(snapshotElement(el, 0, 0)))

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
			// Checkboxes/switches store AXValue as CFNumber; Value() returns "".
			if val == "" {
				role := el.Role()
				if role == "AXCheckBox" || role == "AXSwitch" || role == "AXRadioButton" {
					if el.IsChecked() {
						val = "1"
					} else {
						val = "0"
					}
				}
			}
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

	case "ocr":
		var findQuery string
		jsonOut := false
		layoutOut := false
		layoutCols, layoutRows := 120, 40
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "--find", "-f":
				if i+1 < len(args) {
					findQuery = args[i+1]
					i++
				}
			case "--json", "-j":
				jsonOut = true
			case "--layout", "-l":
				layoutOut = true
			case "--cols":
				if i+1 < len(args) {
					if v, err := strconv.Atoi(args[i+1]); err == nil {
						layoutCols = v
					}
					i++
				}
			case "--rows":
				if i+1 < len(args) {
					if v, err := strconv.Atoi(args[i+1]); err == nil {
						layoutRows = v
					}
					i++
				}
			}
		}

		capture, err := capturePipelineOCRScope(pc)
		if err != nil {
			return err
		}
		results := capture.result

		if findQuery != "" {
			results = findOCRText(results, findQuery)
			if len(results) == 0 {
				return fmt.Errorf("ocr: no text matching %q found", findQuery)
			}
		}
		if layoutOut {
			buf.WriteString(renderOCRLayout(results, capture.imgW, capture.imgH, layoutCols, layoutRows))
		} else if jsonOut {
			s, err := formatOCRResultsJSON(results, capture.target)
			if err != nil {
				return err
			}
			buf.WriteString(s)
			buf.WriteByte('\n')
		} else {
			buf.WriteString(formatOCRResults(results, capture.target))
		}

	case "ocr-hover":
		if len(args) == 0 {
			return fmt.Errorf("ocr-hover: requires text argument")
		}
		capture, err := capturePipelineOCRScope(pc)
		if err != nil {
			return err
		}
		matches := findOCRText(capture.result, strings.Join(args, " "))
		if len(matches) == 0 {
			return fmt.Errorf("ocr-hover: no text matching %q found", strings.Join(args, " "))
		}
		summary, resolutionNote, err := performOCRHover(capture, matches[0])
		if err != nil {
			return err
		}
		fmt.Fprintln(buf, summary)
		if resolutionNote != "" {
			fmt.Fprintln(buf, resolutionNote)
		}

	case "highlight":
		if len(args) == 0 {
			return fmt.Errorf("highlight: requires text argument")
		}
		capture, err := capturePipelineOCRScope(pc)
		if err != nil {
			return err
		}
		query := strings.Join(args, " ")
		rawMatches := findOCRText(capture.result, query)
		if len(rawMatches) == 0 {
			return fmt.Errorf("highlight: no text matching %q found", query)
		}
		count, err := highlightOCRMatches(capture, rawMatches, highlightDuration)
		if err != nil {
			return err
		}
		if count == len(rawMatches) {
			fmt.Fprintf(buf, "highlighted %d OCR match", count)
		} else {
			fmt.Fprintf(buf, "highlighted %d unique OCR match", count)
		}
		if count != 1 {
			buf.WriteByte('e')
			buf.WriteByte('s')
		}
		fmt.Fprintf(buf, " for %q in %s for %s\n", query, capture.desc, highlightDuration)
		if count != len(rawMatches) {
			fmt.Fprintf(buf, "showing %d unique boxes from %d OCR matches\n", count, len(rawMatches))
		}

	case "action":
		if len(args) == 0 {
			return fmt.Errorf("action: requires action name (e.g. AXPress, AXShowMenu)")
		}
		el := pc.element
		if el == nil && len(pc.elements) > 0 {
			el = pc.elements[0]
		}
		if el == nil {
			return fmt.Errorf("action: no element in context")
		}
		actionName := args[0]
		if err := el.PerformAction(actionName); err != nil {
			return fmt.Errorf("action %s: %w", actionName, err)
		}
		// Spin the run loop so the target app processes the action.
		axuiautomation.SpinRunLoop(200 * time.Millisecond)
		fmt.Fprintf(buf, "performed %s on %s\n", actionName, formatSnapshot(snapshotElement(el, 0, 0)))

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
			"  find [--role R] [--title T] [--contains C] [--id I]  (normalized text match)\n"+
			"  ocr [--find TEXT] [--json] [--layout] [--cols N] [--rows N]\n"+
			"  ocr-hover <text>\n"+
			"  highlight <text>\n"+
			"  .\n"+
			"  tree [--depth N]\n"+
			"  list\n"+
			"  json\n"+
			"  click\n"+
			"  rightclick\n"+
			"  click-at <x> <y>\n"+
			"  hover\n"+
			"  type <text>\n"+
			"  action <AXAction>\n"+
			"  attr <AXAttr>\n"+
			"  click-menu <A> <B> <C>", cmd)
	}
	return nil
}
