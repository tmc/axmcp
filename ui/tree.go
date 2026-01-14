package ui

import (
	"fmt"
	"strings"
)

// VisualTree returns a tree representation of the element and its children
// using ASCII box-drawing characters.
func (e *Element) VisualTree() string {
	var sb strings.Builder
	e.dumpVisual(&sb, "", true)
	return sb.String()
}

func (e *Element) dumpVisual(sb *strings.Builder, prefix string, isLast bool) {
	// Current node representation
	// Use box drawing for connection to parent
	connector := ""
	if prefix != "" {
		if isLast {
			connector = "└── "
		} else {
			connector = "├── "
		}
	}

	role := e.Role()
	title := e.Title()
	label := e.Attributes().Label
	id := e.Attributes().Identifier

	desc := role
	if id != "" {
		desc += fmt.Sprintf(" (#%s)", id)
	}
	if label != "" {
		desc += fmt.Sprintf(" [label=%q]", label)
	}
	// Only show title if different from label/id/role to reduce noise
	if title != "" && title != label && title != role {
		desc += fmt.Sprintf(" %q", title)
	}

	sb.WriteString(fmt.Sprintf("%s%s%s\n", prefix, connector, desc))

	// Prepare prefix for children
	childPrefix := prefix
	if prefix == "" {
		childPrefix = ""
	} else {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}

	children := e.Children()
	count := len(children)
	for i, child := range children {
		child.dumpVisual(sb, childPrefix, i == count-1)
	}
}
