package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tmc/apple/x/axuiautomation"
)

type textMatchKind int

const (
	matchNone textMatchKind = iota
	matchContains
	matchExact
)

type searchOptions struct {
	Role       string
	Title      string
	Contains   string
	Identifier string
	Exact      bool
	Limit      int
}

type elementRecord struct {
	role            string
	title           string
	desc            string
	value           string
	identifier      string
	roleDescription string
	enabled         bool
	visible         bool
	actionable      bool
	depth           int
	index           int
	x               int
	y               int
	w               int
	h               int
}

type elementSnapshot struct {
	element *axuiautomation.Element
	record  elementRecord
}

type matchedElement struct {
	snapshot      elementSnapshot
	fieldName     string
	fieldValue    string
	fieldPriority int
	matchKind     textMatchKind
}

type matchResult struct {
	options    searchOptions
	candidates []elementSnapshot
	matches    []matchedElement
}

type clickResolution struct {
	target                elementSnapshot
	viaDescendant         bool
	reason                string
	actionableDescendants []elementSnapshot
}

type matchField struct {
	name     string
	value    string
	priority int
}

const defaultSearchTraversalLimit = 2000

func normalizeMatchString(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func displayString(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func collectSnapshots(root *axuiautomation.Element, limit int) []elementSnapshot {
	if root == nil {
		return nil
	}
	if limit <= 0 {
		limit = 500
	}

	type queueItem struct {
		element *axuiautomation.Element
		depth   int
	}
	queue := []queueItem{{element: root, depth: 0}}
	snapshots := make([]elementSnapshot, 0, limit)
	index := 0
	visited := 0

	for len(queue) > 0 && visited < limit {
		item := queue[0]
		queue = queue[1:]
		if item.element == nil {
			continue
		}
		visited++
		snapshots = append(snapshots, snapshotElement(item.element, item.depth, index))
		index++
		for _, child := range item.element.Children() {
			queue = append(queue, queueItem{element: child, depth: item.depth + 1})
		}
	}
	return snapshots
}

func snapshotElement(element *axuiautomation.Element, depth, index int) elementSnapshot {
	x, y := element.Position()
	w, h := element.Size()
	role := displayString(element.Role())
	// Checkboxes/switches store AXValue as CFNumber; Value() returns "".
	value := displayString(element.Value())
	if value == "" && (role == "AXCheckBox" || role == "AXSwitch" || role == "AXRadioButton") {
		if element.IsChecked() {
			value = "1"
		} else {
			value = "0"
		}
	}
	record := elementRecord{
		role:            role,
		title:           displayString(element.Title()),
		desc:            displayString(element.Description()),
		value:           value,
		identifier:      displayString(element.Identifier()),
		roleDescription: displayString(element.RoleDescription()),
		enabled:         element.IsEnabled(),
		visible:         w > 0 && h > 0,
		actionable:      isActionableRole(role),
		depth:           depth,
		index:           index,
		x:               x,
		y:               y,
		w:               w,
		h:               h,
	}
	return elementSnapshot{element: element, record: record}
}

func isActionableRole(role string) bool {
	switch role {
	case "AXButton",
		"AXCheckBox",
		"AXDisclosureTriangle",
		"AXLink",
		"AXMenuBarItem",
		"AXMenuButton",
		"AXMenuItem",
		"AXOutlineRow",
		"AXPopUpButton",
		"AXRadioButton",
		"AXRow",
		"AXSearchField",
		"AXSecureTextField",
		"AXSegment",
		"AXSlider",
		"AXSwitch",
		"AXTab",
		"AXTextField":
		return true
	}
	return strings.HasSuffix(role, "Button") || strings.HasSuffix(role, "Item")
}

func orderedFields(record elementRecord) []matchField {
	return []matchField{
		{name: "title", value: record.title, priority: 0},
		{name: "desc", value: record.desc, priority: 1},
		{name: "value", value: record.value, priority: 2},
		{name: "identifier", value: record.identifier, priority: 3},
		{name: "role description", value: record.roleDescription, priority: 4},
	}
}

func matchTextField(record elementRecord, query string, exactOnly bool) (matchField, textMatchKind, bool) {
	query = normalizeMatchString(query)
	if query == "" {
		return matchField{}, matchNone, true
	}

	fields := orderedFields(record)
	for _, field := range fields {
		if normalizeMatchString(field.value) == query {
			return field, matchExact, true
		}
	}
	if exactOnly {
		return matchField{}, matchNone, false
	}
	for _, field := range fields {
		if norm := normalizeMatchString(field.value); norm != "" && strings.Contains(norm, query) {
			return field, matchContains, true
		}
	}
	return matchField{}, matchNone, false
}

func matchesRole(record elementRecord, role string) bool {
	role = normalizeMatchString(role)
	if role == "" {
		return true
	}
	return normalizeMatchString(record.role) == role
}

func matchElement(snapshot elementSnapshot, options searchOptions) (matchedElement, bool) {
	record := snapshot.record
	if !matchesRole(record, options.Role) {
		return matchedElement{}, false
	}
	if options.Identifier != "" && normalizeMatchString(record.identifier) != normalizeMatchString(options.Identifier) {
		return matchedElement{}, false
	}

	field, kind, ok := matchField{}, matchNone, true
	switch {
	case options.Title != "":
		field, kind, ok = matchTextField(record, options.Title, true)
	case options.Contains != "":
		field, kind, ok = matchTextField(record, options.Contains, options.Exact)
	}
	if !ok {
		return matchedElement{}, false
	}

	return matchedElement{
		snapshot:      snapshot,
		fieldName:     field.name,
		fieldValue:    field.value,
		fieldPriority: field.priority,
		matchKind:     kind,
	}, true
}

// isMenuRole returns true for roles that belong to the system menu bar.
// These are deprioritized during ranking so in-window elements are preferred.
func isMenuRole(role string) bool {
	switch role {
	case "AXMenuBar", "AXMenuBarItem", "AXMenu", "AXMenuItem":
		return true
	}
	return false
}

func compareMatches(a, b matchedElement) bool {
	ar := a.snapshot.record
	br := b.snapshot.record
	aMenu := isMenuRole(ar.role)
	bMenu := isMenuRole(br.role)
	aContent := webContentPriority(a)
	bContent := webContentPriority(b)
	switch {
	case aMenu != bMenu:
		return !aMenu // prefer non-menu elements
	case aContent != bContent:
		return aContent > bContent
	case ar.enabled != br.enabled:
		return ar.enabled
	case ar.visible != br.visible:
		return ar.visible
	case ar.actionable != br.actionable:
		return ar.actionable
	case ar.depth != br.depth:
		return ar.depth > br.depth
	case a.matchKind != b.matchKind:
		return a.matchKind > b.matchKind
	case a.fieldPriority != b.fieldPriority:
		return a.fieldPriority < b.fieldPriority
	default:
		return ar.index < br.index
	}
}

func webContentPriority(match matchedElement) int {
	record := match.snapshot.record
	if isBrowserChromePageProxy(record) {
		return -1
	}
	return 0
}

func isBrowserChromePageProxy(record elementRecord) bool {
	switch record.role {
	case "AXRadioButton", "AXTab":
		return true
	}
	return false
}

func candidateScore(record elementRecord, query string) int {
	query = normalizeMatchString(query)
	if query == "" {
		return 0
	}
	best := 0
	tokens := strings.Fields(query)
	for _, field := range orderedFields(record) {
		norm := normalizeMatchString(field.value)
		if norm == "" {
			continue
		}
		score := 0
		for _, token := range tokens {
			if strings.Contains(norm, token) {
				score++
			}
		}
		if score > best {
			best = score
		}
	}
	return best
}

func primaryQuery(options searchOptions) string {
	switch {
	case options.Title != "":
		return options.Title
	case options.Contains != "":
		return options.Contains
	case options.Identifier != "":
		return options.Identifier
	default:
		return ""
	}
}

func searchTraversalLimit(limit int) int {
	if limit <= 0 || limit < defaultSearchTraversalLimit {
		return defaultSearchTraversalLimit
	}
	return limit
}

func matchElementsFromSnapshots(snapshots []elementSnapshot, options searchOptions) matchResult {
	result := matchResult{
		options:    options,
		candidates: make([]elementSnapshot, 0, len(snapshots)),
	}
	for _, snapshot := range snapshots {
		if !matchesRole(snapshot.record, options.Role) {
			continue
		}
		result.candidates = append(result.candidates, snapshot)
		if match, ok := matchElement(snapshot, options); ok {
			result.matches = append(result.matches, match)
		}
	}
	sort.SliceStable(result.matches, func(i, j int) bool {
		return compareMatches(result.matches[i], result.matches[j])
	})
	if options.Limit > 0 && len(result.matches) > options.Limit {
		result.matches = result.matches[:options.Limit]
	}
	return result
}

func findElements(root *axuiautomation.Element, options searchOptions) matchResult {
	snapshots := collectSnapshots(root, searchTraversalLimit(options.Limit))
	return matchElementsFromSnapshots(snapshots, options)
}

func formatRecord(record elementRecord) string {
	parts := []string{record.role}
	if record.title != "" {
		parts = append(parts, fmt.Sprintf("title=%q", record.title))
	}
	if record.desc != "" && record.desc != record.title {
		parts = append(parts, fmt.Sprintf("desc=%q", record.desc))
	}
	if record.value != "" && record.value != record.title && record.value != record.desc {
		parts = append(parts, fmt.Sprintf("value=%q", record.value))
	}
	if record.identifier != "" {
		parts = append(parts, fmt.Sprintf("id=%q", record.identifier))
	}
	parts = append(parts, fmt.Sprintf("bounds=(%d,%d %dx%d)", record.x, record.y, record.w, record.h))
	return strings.Join(parts, " ")
}

func formatSnapshot(snapshot elementSnapshot) string {
	return formatRecord(snapshot.record)
}

func formatMatch(match matchedElement) string {
	return formatSnapshot(match.snapshot)
}

func shortlistCandidates(result matchResult, n int) []elementSnapshot {
	candidates := append([]elementSnapshot(nil), result.candidates...)
	query := primaryQuery(result.options)
	sort.SliceStable(candidates, func(i, j int) bool {
		ri := candidates[i].record
		rj := candidates[j].record
		si := candidateScore(ri, query)
		sj := candidateScore(rj, query)
		switch {
		case si != sj:
			return si > sj
		case ri.enabled != rj.enabled:
			return ri.enabled
		case ri.visible != rj.visible:
			return ri.visible
		case ri.actionable != rj.actionable:
			return ri.actionable
		case ri.depth != rj.depth:
			return ri.depth > rj.depth
		default:
			return ri.index < rj.index
		}
	})
	if n > len(candidates) {
		n = len(candidates)
	}
	return candidates[:n]
}

func describeSearch(options searchOptions) string {
	var parts []string
	switch {
	case options.Title != "":
		parts = append(parts, fmt.Sprintf("exact match %q", options.Title))
	case options.Contains != "" && options.Exact:
		parts = append(parts, fmt.Sprintf("exact match %q", options.Contains))
	case options.Contains != "":
		parts = append(parts, fmt.Sprintf("containing %q", options.Contains))
	}
	if options.Identifier != "" {
		parts = append(parts, fmt.Sprintf("id %q", options.Identifier))
	}
	if options.Role != "" {
		parts = append(parts, fmt.Sprintf("role %q", options.Role))
	}
	if len(parts) == 0 {
		return "element"
	}
	return "element " + strings.Join(parts, ", ")
}

// hasWebContent checks if any candidate in the result set is a web area,
// indicating the UI is rendered via a webview (e.g. Electron) where AX
// visibility is limited.
func hasWebContent(result matchResult) bool {
	for _, c := range result.candidates {
		if c.record.role == "AXWebArea" {
			return true
		}
	}
	return false
}

func noMatchMessage(result matchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s not found", describeSearch(result.options))
	candidates := shortlistCandidates(result, 5)
	if len(candidates) == 0 {
		if hasWebContent(result) {
			b.WriteString(webContentHint)
		}
		return b.String()
	}
	b.WriteString(". Candidates:\n")
	for _, candidate := range candidates {
		fmt.Fprintf(&b, "  - %s\n", formatSnapshot(candidate))
	}
	if hasWebContent(result) {
		b.WriteString(webContentHint)
	}
	return strings.TrimRight(b.String(), "\n")
}

const webContentHint = "\nNote: AXWebArea detected — this app uses a webview (e.g. Electron). " +
	"Web-rendered UI elements may not appear in the AX tree. " +
	"Use CDP tools (Chrome DevTools Protocol) for web content interaction."

func selectionReason(result matchResult) string {
	if len(result.matches) <= 1 {
		return ""
	}
	match := result.matches[0]
	var reasons []string
	if match.matchKind == matchExact {
		reasons = append(reasons, "exact "+match.fieldName+" match")
	} else if match.matchKind == matchContains {
		reasons = append(reasons, match.fieldName+" substring match")
	}
	if match.snapshot.record.enabled {
		reasons = append(reasons, "enabled")
	}
	if match.snapshot.record.visible {
		reasons = append(reasons, "visible")
	}
	if match.snapshot.record.actionable {
		reasons = append(reasons, "interactive")
	}
	reasons = append(reasons, fmt.Sprintf("depth=%d", match.snapshot.record.depth))
	return fmt.Sprintf("selected [0] from %d matches: %s", len(result.matches), strings.Join(reasons, ", "))
}

func actionableDescendants(snapshot elementSnapshot, limit int) []elementSnapshot {
	if snapshot.element == nil {
		return nil
	}
	rootRef := snapshot.element.Ref()
	descendants := collectSnapshots(snapshot.element, limit)
	filtered := make([]elementSnapshot, 0, len(descendants))
	for _, descendant := range descendants {
		if descendant.element == nil || descendant.element.Ref() == rootRef {
			continue
		}
		record := descendant.record
		if record.actionable && record.enabled && record.visible {
			filtered = append(filtered, descendant)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		ri := filtered[i].record
		rj := filtered[j].record
		switch {
		case ri.depth != rj.depth:
			return ri.depth > rj.depth
		case ri.index != rj.index:
			return ri.index < rj.index
		default:
			return formatSnapshot(filtered[i]) < formatSnapshot(filtered[j])
		}
	})
	return filtered
}

func resolveClickTarget(match matchedElement, limit int) clickResolution {
	return resolveClickTargetFromDescendants(match, actionableDescendants(match.snapshot, limit))
}

func resolveClickTargetFromDescendants(match matchedElement, descendants []elementSnapshot) clickResolution {
	record := match.snapshot.record
	if record.actionable && record.enabled && record.visible {
		return clickResolution{target: match.snapshot}
	}
	resolution := clickResolution{
		target:                match.snapshot,
		actionableDescendants: descendants,
	}
	if len(descendants) == 1 {
		resolution.target = descendants[0]
		resolution.viaDescendant = true
		resolution.reason = fmt.Sprintf("matched %s is not directly actionable; using single actionable descendant %s",
			formatMatch(match), formatSnapshot(descendants[0]))
	}
	return resolution
}
