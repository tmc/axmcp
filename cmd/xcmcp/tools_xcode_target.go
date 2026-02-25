package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tmc/xcmcp/axuiautomation"
)

func registerXcodeTargetTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "xcode_add_target",
		Description: "Add a new target to the current Xcode project by driving the File > New > Target wizard via accessibility automation.",
	}, SafeTool("xcode_add_target", func(ctx context.Context, req *mcp.CallToolRequest, args struct {
		TemplateName string `json:"template_name" description:"Target template name (e.g. 'Widget Extension', 'App Intent Extension')"`
		ProductName  string `json:"product_name" description:"Product name for the new target"`
		BundleID     string `json:"bundle_id,omitempty" description:"Bundle identifier (auto-derived if not specified)"`
		Team         string `json:"team,omitempty" description:"Development team name to select"`
	}) (*mcp.CallToolResult, SimulatorActionOutput, error) {
		if args.TemplateName == "" {
			return errResult("template_name is required"), SimulatorActionOutput{}, nil
		}
		if args.ProductName == "" {
			return errResult("product_name is required"), SimulatorActionOutput{}, nil
		}

		app, err := axuiautomation.NewApplication("com.apple.dt.Xcode")
		if err != nil {
			return errResult("Xcode is not running: " + err.Error()), SimulatorActionOutput{}, nil
		}
		defer app.Close()

		if err := app.Activate(); err != nil {
			return errResult("failed to activate Xcode: " + err.Error()), SimulatorActionOutput{}, nil
		}
		time.Sleep(300 * time.Millisecond)

		// Step 1: Ensure a project-level item is selected in the navigator so
		// that File > New > Target... is available. We try to click the root
		// item of the Project Navigator (the .xcodeproj node).
		ensureProjectNodeSelected(app)
		time.Sleep(200 * time.Millisecond)

		// Step 2: Open File > New > Target… (try both ellipsis variants).
		var menuErr error
		for _, name := range []string{"Target\u2026", "Target..."} {
			menuErr = app.ClickMenuItem([]string{"File", "New", name})
			if menuErr == nil {
				break
			}
		}
		if menuErr != nil {
			return errResult(fmt.Sprintf("failed to open File > New > Target: %v", menuErr)), SimulatorActionOutput{}, nil
		}

		// Step 2: Wait for the template chooser sheet/window.
		sheet, err := waitForXcodeSheet(app, 8*time.Second)
		if err != nil {
			return errResult("template chooser did not appear: " + err.Error()), SimulatorActionOutput{}, nil
		}

		// Step 3: Find and click the template by name.
		if err := selectTemplate(sheet, args.TemplateName); err != nil {
			axuiautomation.SendEscape()
			return errResult(fmt.Sprintf("failed to select template %q: %v", args.TemplateName, err)), SimulatorActionOutput{}, nil
		}

		// Step 4: Click "Next".
		if err := clickButton(sheet, "Next", 3*time.Second); err != nil {
			axuiautomation.SendEscape()
			return errResult("failed to click Next: " + err.Error()), SimulatorActionOutput{}, nil
		}
		time.Sleep(300 * time.Millisecond)

		// The sheet is still the same window; re-fetch the focused sheet.
		sheet2, err := waitForXcodeSheet(app, 5*time.Second)
		if err != nil {
			// Fall back to using app root.
			sheet2 = app.Root()
		}

		// Step 5: Fill Product Name.
		if err := fillTextField(sheet2, "Product Name", args.ProductName); err != nil {
			axuiautomation.SendEscape()
			return errResult("failed to fill product name: " + err.Error()), SimulatorActionOutput{}, nil
		}

		// Step 6: Optionally fill Bundle Identifier.
		if args.BundleID != "" {
			_ = fillTextField(sheet2, "Bundle Identifier", args.BundleID)
		}

		// Step 7: Optionally select Team.
		if args.Team != "" {
			_ = selectTeam(sheet2, args.Team)
		}

		// Step 8: Click "Finish".
		if err := clickButton(sheet2, "Finish", 3*time.Second); err != nil {
			axuiautomation.SendEscape()
			return errResult("failed to click Finish: " + err.Error()), SimulatorActionOutput{}, nil
		}

		// Step 9: Handle "Activate scheme?" dialog if it appears.
		time.Sleep(500 * time.Millisecond)
		handleActivateSchemeDialog(app)

		msg := fmt.Sprintf("added target %q (template: %s)", args.ProductName, args.TemplateName)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		}, SimulatorActionOutput{Message: msg}, nil
	}))
}

// waitForXcodeSheet waits for an AXSheet child of the main Xcode window to appear.
// Falls back to waiting for any new window if no sheet is found.
func waitForXcodeSheet(app *axuiautomation.Application, timeout time.Duration) (*axuiautomation.Element, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Prefer an AXSheet attached to the main window.
		sheets := app.Sheets().AllElements()
		if len(sheets) > 0 {
			return sheets[0], nil
		}
		// Also accept a standalone AXDialog window.
		dialogs := app.Dialogs().AllElements()
		if len(dialogs) > 0 {
			return dialogs[0], nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, axuiautomation.ErrTimeout
}

// selectTemplate finds the named template cell and double-clicks it (or clicks + verifies selection).
func selectTemplate(sheet *axuiautomation.Element, name string) error {
	// Template names appear as AXStaticText inside collection/grid cells.
	// We try multiple passes: direct title match, then description match, then scroll-and-search.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Direct: find by title anywhere in the sheet tree.
		el := sheet.Descendants().ByTitle(name).First()
		if el != nil {
			if err := el.ScrollToVisible(); err == nil {
				time.Sleep(50 * time.Millisecond)
			}
			if err := el.Click(); err != nil {
				return fmt.Errorf("clicking template: %w", err)
			}
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		// Partial match on static text.
		el = sheet.Descendants().ByRole("AXStaticText").Matching(func(e *axuiautomation.Element) bool {
			return strings.Contains(e.Title(), name) || strings.Contains(e.Value(), name)
		}).First()
		if el != nil {
			if err := el.ScrollToVisible(); err == nil {
				time.Sleep(50 * time.Millisecond)
			}
			if err := el.Click(); err != nil {
				return fmt.Errorf("clicking template text: %w", err)
			}
			time.Sleep(100 * time.Millisecond)
			return nil
		}

		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("template %q not found in chooser", name)
}

// clickButton finds a button by title within the element and clicks it.
// Retries until timeout if the button is not yet enabled.
func clickButton(parent *axuiautomation.Element, title string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		btn := parent.Descendants().ByRole("AXButton").ByTitle(title).First()
		if btn != nil {
			if btn.IsEnabled() {
				return btn.Click()
			}
			btn.Release()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("button %q not found or not enabled within timeout", title)
}

// fillTextField finds a labelled text field and sets its value to text.
// It searches for a text field near a label with the given name.
func fillTextField(parent *axuiautomation.Element, label, text string) error {
	// Try finding a text field whose AXIdentifier or placeholder matches label.
	tf := parent.Descendants().ByRole("AXTextField").Matching(func(e *axuiautomation.Element) bool {
		id := e.Identifier()
		placeholder := e.Description()
		return strings.EqualFold(id, label) ||
			strings.EqualFold(placeholder, label) ||
			strings.EqualFold(e.Title(), label)
	}).First()

	if tf == nil {
		// Fall back: find the static text label and use the next sibling text field.
		tf = findTextFieldAfterLabel(parent, label)
	}

	if tf == nil {
		return fmt.Errorf("text field for %q not found", label)
	}
	defer tf.Release()

	// Clear and type the value.
	if err := tf.Focus(); err != nil {
		if err2 := tf.Click(); err2 != nil {
			return fmt.Errorf("focusing text field: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Select all and replace.
	if err := axuiautomation.SendKeyCombo(0x00, false, false, false, true); err == nil {
		// Cmd+A to select all
		time.Sleep(30 * time.Millisecond)
	}
	return tf.TypeText(text)
}

// findTextFieldAfterLabel searches for a label element then returns the first
// AXTextField that appears nearby (as a sibling in the same parent group).
func findTextFieldAfterLabel(parent *axuiautomation.Element, label string) *axuiautomation.Element {
	// Walk all group/row containers looking for the label.
	var result *axuiautomation.Element
	parent.Descendants().ByRole("AXStaticText").ByTitle(label).ForEach(func(labelEl *axuiautomation.Element) bool {
		// Check the label's parent for sibling text fields.
		p := labelEl.Parent()
		if p == nil {
			return true
		}
		defer p.Release()
		tf := p.Descendants().ByRole("AXTextField").First()
		if tf != nil {
			result = tf
			return false // stop
		}
		return true
	})
	return result
}

// selectTeam finds the Team popup button and selects the given team name.
func selectTeam(parent *axuiautomation.Element, team string) error {
	popup := parent.Descendants().ByRole("AXPopUpButton").Matching(func(e *axuiautomation.Element) bool {
		id := strings.ToLower(e.Identifier())
		title := strings.ToLower(e.Title())
		desc := strings.ToLower(e.Description())
		return strings.Contains(id, "team") || strings.Contains(title, "team") || strings.Contains(desc, "team")
	}).First()
	if popup == nil {
		return fmt.Errorf("team popup not found")
	}
	defer popup.Release()
	return popup.SelectMenuItem(team)
}

// handleActivateSchemeDialog looks for an "Activate" or "Don't Activate" sheet
// after adding a target and clicks "Activate" by default.
func handleActivateSchemeDialog(app *axuiautomation.Application) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sheets := app.Sheets().AllElements()
		for _, sheet := range sheets {
			if btn := sheet.Buttons().ByTitle("Activate").First(); btn != nil {
				_ = btn.Click()
				return
			}
			if btn := sheet.Buttons().ByTitle("Don't Activate").First(); btn != nil {
				_ = btn.Click()
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ensureProjectNodeSelected tries to click the root .xcodeproj or .xcworkspace
// node in the Project Navigator so that File > New > Target... is available.
// Xcode only shows "Target..." in the New submenu when a project-level item is
// selected — not when a source file is selected.
func ensureProjectNodeSelected(app *axuiautomation.Application) {
	// Find the navigator sidebar. It is typically an AXSplitGroup containing
	// an AXOutline (the project tree).
	win := app.Windows().First()
	if win == nil {
		return
	}
	defer win.Release()

	// The project navigator outline contains a row whose title ends with
	// ".xcodeproj" or ".xcworkspace". Click the first such row.
	outline := win.Descendants().ByRole("AXOutline").First()
	if outline == nil {
		return
	}
	defer outline.Release()

	row := outline.Descendants().ByRole("AXRow").Matching(func(e *axuiautomation.Element) bool {
		t := e.Title()
		return strings.HasSuffix(t, ".xcodeproj") || strings.HasSuffix(t, ".xcworkspace")
	}).First()
	if row == nil {
		// Fall back: click the very first row in the outline.
		row = outline.Descendants().ByRole("AXRow").First()
	}
	if row != nil {
		_ = row.Click()
		row.Release()
	}
}

func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}
