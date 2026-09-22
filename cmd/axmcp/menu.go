package main

import (
	"fmt"
	"strings"

	"github.com/tmc/apple/x/axuiautomation"
)

// menuItemState describes a menu item as read from the AXMenuBar hierarchy.
// Reading never presses an item, so it neither opens a menu nor raises the
// owning window.
type menuItemState struct {
	Title      string `json:"title"`
	Enabled    bool   `json:"enabled"`
	Separator  bool   `json:"separator,omitempty"`
	HasSubmenu bool   `json:"has_submenu,omitempty"`
}

// menuRoles are the roles that make up the menu hierarchy. AXMenu appears as
// the container a menu bar item owns; its children are the AXMenuItems.
func isMenuContainerRole(role string) bool {
	return role == "AXMenuBar" || role == "AXMenu"
}

// menuChildren returns the menu entries owned by parent, descending through the
// AXMenu container that a menu bar item or submenu item holds.
//
// The caller owns the returned elements and must release them.
func menuChildren(parent *axuiautomation.Element) []*axuiautomation.Element {
	if parent == nil {
		return nil
	}
	children := parent.Children()
	// A menu bar item or a submenu-bearing item owns a single AXMenu child that
	// holds the actual items. Descend through it.
	if !isMenuContainerRole(parent.Role()) {
		var menu *axuiautomation.Element
		var rest []*axuiautomation.Element
		for _, child := range children {
			if menu == nil && child.Role() == "AXMenu" {
				menu = child
				continue
			}
			rest = append(rest, child)
		}
		for _, child := range rest {
			child.Release()
		}
		if menu == nil {
			return nil
		}
		items := menu.Children()
		menu.Release()
		return items
	}
	return children
}

// findMenuChild returns the child of parent whose title matches title, or nil.
// Matching is normalized, so "Export..." finds the "Export…" that macOS
// actually publishes.
func findMenuChild(parent *axuiautomation.Element, title string) *axuiautomation.Element {
	want := normalizeMatchString(title)
	var found *axuiautomation.Element
	for _, child := range menuChildren(parent) {
		if found == nil && normalizeMatchString(child.Title()) == want {
			found = child
			continue
		}
		child.Release()
	}
	return found
}

// readMenuPath walks path through the menu hierarchy without pressing anything
// and returns the element it names. The caller owns the result.
func readMenuPath(app *axuiautomation.Application, path []string) (*axuiautomation.Element, error) {
	if app == nil {
		return nil, fmt.Errorf("no application")
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("empty menu path")
	}
	menuBar := app.MenuBar()
	if menuBar == nil {
		return nil, fmt.Errorf("menu bar not found")
	}
	current := menuBar
	for i, name := range path {
		child := findMenuChild(current, name)
		current.Release()
		if child == nil {
			return nil, fmt.Errorf("menu item %q not found under %q", name, strings.Join(path[:i], " > "))
		}
		current = child
	}
	return current, nil
}

// clickMenuPath presses the menu item named by path. Titles match as in
// readMenuPath, so "Export..." presses the "Export…" that macOS publishes.
func clickMenuPath(app *axuiautomation.Application, path []string) error {
	titles := make([]string, len(path))
	for i := range path {
		el, err := readMenuPath(app, path[:i+1])
		if err != nil {
			return err
		}
		titles[i] = el.Title()
		el.Release()
	}
	return app.ClickMenuItem(titles)
}

// readMenuItem reports the state of the menu item named by path. It presses
// nothing: no menu opens and no window is raised.
func readMenuItem(app *axuiautomation.Application, path []string) (menuItemState, error) {
	el, err := readMenuPath(app, path)
	if err != nil {
		return menuItemState{}, err
	}
	defer el.Release()
	return menuItemStateOf(el), nil
}

// listMenuItems returns the entries directly under the menu named by path, or
// the menu bar itself when path is empty. It presses nothing.
func listMenuItems(app *axuiautomation.Application, path []string) ([]menuItemState, error) {
	if app == nil {
		return nil, fmt.Errorf("no application")
	}
	var parent *axuiautomation.Element
	if len(path) == 0 {
		if parent = app.MenuBar(); parent == nil {
			return nil, fmt.Errorf("menu bar not found")
		}
	} else {
		var err error
		if parent, err = readMenuPath(app, path); err != nil {
			return nil, err
		}
	}
	defer parent.Release()

	var out []menuItemState
	for _, child := range menuChildren(parent) {
		out = append(out, menuItemStateOf(child))
		child.Release()
	}
	return out, nil
}

// menuItemStateOf reads one item's state. A separator has no title.
func menuItemStateOf(el *axuiautomation.Element) menuItemState {
	title := displayString(el.Title())
	state := menuItemState{
		Title:     title,
		Enabled:   el.IsEnabled(),
		Separator: title == "",
	}
	for _, child := range el.Children() {
		if !state.HasSubmenu && child.Role() == "AXMenu" {
			state.HasSubmenu = true
		}
		child.Release()
	}
	return state
}

// formatMenuItems renders menu entries for humans.
func formatMenuItems(items []menuItemState) string {
	var b strings.Builder
	for _, it := range items {
		if it.Separator {
			b.WriteString("  ---\n")
			continue
		}
		fmt.Fprintf(&b, "  %s", it.Title)
		if !it.Enabled {
			b.WriteString(" (disabled)")
		}
		if it.HasSubmenu {
			b.WriteString(" >")
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "(no items)\n"
	}
	return b.String()
}
