//go:build darwin

// Package xcodewizard drives Xcode's File > New > Target wizard through the
// macOS Accessibility API. It is scoped to that wizard and does not attempt to
// be a general-purpose UI automation layer; use the axuiautomation package
// directly for anything outside the target creation flow.
package xcodewizard

import (
	"fmt"
	"strings"
	"time"

	"github.com/tmc/apple/x/axuiautomation"
)

// XcodeBundleID is the bundle identifier of the Xcode application.
const XcodeBundleID = "com.apple.dt.Xcode"

// Options describes a new Xcode target to add via File > New > Target.
type Options struct {
	// TemplateName is the title of the target template to select, e.g.
	// "Widget Extension" or "App Intent Extension".
	TemplateName string

	// ProductName is filled into the "Product Name" field on the configuration
	// sheet. It becomes the target and scheme name.
	ProductName string

	// BundleID, if non-empty, is typed into the "Bundle Identifier" field.
	// When empty, Xcode auto-derives it from the product name.
	BundleID string

	// Team, if non-empty, is selected in the "Team" popup.
	Team string

	// Platform, if non-empty, selects the platform tab at the top of the
	// template chooser (e.g. "iOS", "macOS", "watchOS", "tvOS", "visionOS",
	// "Multiplatform"). Widget Extension appears under several platforms, so
	// this disambiguates the match.
	Platform string

	// EmbedIn, if non-empty, is selected in the "Embed in Application" popup
	// that appears on the configuration sheet when a project contains more
	// than one embeddable host target.
	EmbedIn string
}

// AddTarget drives the File > New > Target wizard in the given Xcode
// application instance. The caller owns the lifecycle of app.
func AddTarget(app *axuiautomation.Application, opts Options) error {
	if opts.TemplateName == "" {
		return fmt.Errorf("template name is required")
	}
	if opts.ProductName == "" {
		return fmt.Errorf("product name is required")
	}

	if err := app.Activate(); err != nil {
		return fmt.Errorf("activate Xcode: %w", err)
	}
	time.Sleep(600 * time.Millisecond)

	sheet, _ := waitForSheet(app, 300*time.Millisecond)
	if sheet == nil {
		ensureProjectNodeSelected(app)
		time.Sleep(300 * time.Millisecond)

		if err := openFileNewTarget(app); err != nil {
			return err
		}
		var err error
		sheet, err = waitForSheet(app, 8*time.Second)
		if err != nil {
			return fmt.Errorf("template chooser did not appear: %w", err)
		}
	}

	if opts.Platform != "" {
		if err := selectPlatformTab(sheet, opts.Platform); err != nil {
			axuiautomation.SendEscape()
			return fmt.Errorf("select platform %q: %w", opts.Platform, err)
		}
		time.Sleep(300 * time.Millisecond)
	}

	_ = typeIntoSearchField(sheet, opts.TemplateName)
	time.Sleep(800 * time.Millisecond)

	if err := clickTemplateByName(sheet, opts.TemplateName); err != nil {
		axuiautomation.SendEscape()
		return fmt.Errorf("select template %q: %w", opts.TemplateName, err)
	}

	if err := clickButton(sheet, "Next", 3*time.Second); err != nil {
		axuiautomation.SendEscape()
		return fmt.Errorf("click Next: %w", err)
	}
	time.Sleep(300 * time.Millisecond)

	sheet2, err := waitForSheet(app, 5*time.Second)
	if err != nil {
		sheet2 = app.Root()
	}

	if err := fillField(sheet2, "Product Name", opts.ProductName); err != nil {
		axuiautomation.SendEscape()
		return fmt.Errorf("fill product name: %w", err)
	}
	if opts.BundleID != "" {
		_ = fillField(sheet2, "Bundle Identifier", opts.BundleID)
	}
	if opts.Team != "" {
		_ = selectPopup(sheet2, "team", opts.Team)
	}
	if opts.EmbedIn != "" {
		_ = selectPopup(sheet2, "embed", opts.EmbedIn)
	}

	if err := clickButton(sheet2, "Finish", 3*time.Second); err != nil {
		axuiautomation.SendEscape()
		return fmt.Errorf("click Finish: %w", err)
	}

	time.Sleep(500 * time.Millisecond)
	dismissActivateScheme(app)
	return nil
}

// openFileNewTarget clicks File > New > Target, trying both the Unicode
// ellipsis and ASCII "..." variants that different Xcode versions use.
func openFileNewTarget(app *axuiautomation.Application) error {
	var err error
	for _, name := range []string{"Target…", "Target..."} {
		err = app.ClickMenuItem([]string{"File", "New", name})
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("open File > New > Target: %w", err)
}

// waitForSheet waits for an AXSheet or AXDialog attached to Xcode.
func waitForSheet(app *axuiautomation.Application, timeout time.Duration) (*axuiautomation.Element, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if sheets := app.Sheets().AllElements(); len(sheets) > 0 {
			return sheets[0], nil
		}
		if dialogs := app.Dialogs().AllElements(); len(dialogs) > 0 {
			return dialogs[0], nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, axuiautomation.ErrTimeout
}

// typeIntoSearchField types into the template chooser's search/filter field so
// that the grid collapses to matching templates.
func typeIntoSearchField(sheet *axuiautomation.Element, text string) error {
	var tf *axuiautomation.Element
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tf = sheet.Descendants().ByRole("AXSearchField").First()
		if tf != nil {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if tf == nil {
		return fmt.Errorf("search field not found")
	}
	defer tf.Release()
	if err := tf.Click(); err != nil {
		return fmt.Errorf("click search field: %w", err)
	}
	time.Sleep(150 * time.Millisecond)
	return tf.TypeText(text)
}

// clickTemplateByName scans the chooser for a descendant whose title or value
// contains the template name and clicks it.
func clickTemplateByName(sheet *axuiautomation.Element, name string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var found *axuiautomation.Element
		sheet.Descendants().ForEach(func(e *axuiautomation.Element) bool {
			t, v := e.Title(), e.Value()
			if strings.Contains(t, name) || strings.Contains(v, name) {
				found = e
				return false
			}
			return true
		})
		if found != nil {
			_ = found.ScrollToVisible()
			err := found.Click()
			found.Release()
			return err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("template %q not found", name)
}

// clickButton finds and clicks an enabled AXButton by title.
func clickButton(parent *axuiautomation.Element, title string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if btn := parent.Descendants().ByRole("AXButton").ByTitle(title).First(); btn != nil {
			if btn.IsEnabled() {
				err := btn.Click()
				btn.Release()
				return err
			}
			btn.Release()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("button %q not found or not enabled", title)
}

// fillField locates a labelled text field and replaces its value with text.
func fillField(parent *axuiautomation.Element, label, text string) error {
	tf := parent.Descendants().ByRole("AXTextField").Matching(func(e *axuiautomation.Element) bool {
		return strings.EqualFold(e.Identifier(), label) ||
			strings.EqualFold(e.Title(), label) ||
			strings.EqualFold(e.Description(), label)
	}).First()
	if tf == nil {
		tf = parent.Descendants().ByRole("AXTextField").Matching(func(e *axuiautomation.Element) bool {
			return e.Role() != "AXSearchField"
		}).First()
	}
	if tf == nil {
		return fmt.Errorf("text field %q not found", label)
	}
	defer tf.Release()
	if err := tf.Focus(); err != nil {
		if err2 := tf.Click(); err2 != nil {
			return fmt.Errorf("focus %q: %w", label, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = axuiautomation.SendKeyCombo(0x00, false, false, false, true) // Cmd+A
	time.Sleep(30 * time.Millisecond)
	return tf.TypeText(text)
}

// selectPopup selects a value in a popup button whose identifier or title
// contains labelHint.
func selectPopup(parent *axuiautomation.Element, labelHint, value string) error {
	hint := strings.ToLower(labelHint)
	popup := parent.Descendants().ByRole("AXPopUpButton").Matching(func(e *axuiautomation.Element) bool {
		return strings.Contains(strings.ToLower(e.Identifier()), hint) ||
			strings.Contains(strings.ToLower(e.Title()), hint) ||
			strings.Contains(strings.ToLower(e.Description()), hint)
	}).First()
	if popup == nil {
		return fmt.Errorf("popup %q not found", labelHint)
	}
	defer popup.Release()
	return popup.SelectMenuItem(value)
}

// selectPlatformTab clicks the platform filter tab at the top of the
// template chooser (e.g. "iOS", "macOS"). The tab row contains AXRadioButton
// cells under an AXRadioGroup in current Xcode versions; older versions used
// AXButton, so we fall back to either.
func selectPlatformTab(sheet *axuiautomation.Element, platform string) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, role := range []string{"AXRadioButton", "AXButton"} {
			el := sheet.Descendants().ByRole(role).Matching(func(e *axuiautomation.Element) bool {
				return strings.EqualFold(e.Title(), platform)
			}).First()
			if el != nil {
				_ = el.ScrollToVisible()
				err := el.Click()
				el.Release()
				return err
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("platform tab %q not found", platform)
}

// ensureProjectNodeSelected clicks the root .xcodeproj or .xcworkspace node in
// the Project Navigator so that File > New > Target... is enabled.
func ensureProjectNodeSelected(app *axuiautomation.Application) {
	win := app.Windows().First()
	if win == nil {
		return
	}
	defer win.Release()
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
		row = outline.Descendants().ByRole("AXRow").First()
	}
	if row != nil {
		_ = row.Click()
		row.Release()
	}
}

// dismissActivateScheme clicks "Activate" on the post-creation sheet if it
// appears; otherwise returns after the timeout.
func dismissActivateScheme(app *axuiautomation.Application) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, sheet := range app.Sheets().AllElements() {
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
