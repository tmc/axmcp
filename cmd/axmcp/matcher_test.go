package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestNormalizeMatchString(t *testing.T) {
	got := normalizeMatchString("  Mesh \n  Settings\tPane  ")
	want := "mesh settings pane"
	if got != want {
		t.Fatalf("normalizeMatchString = %q, want %q", got, want)
	}
}

func TestMatchElementFallsBackAcrossFields(t *testing.T) {
	tests := []struct {
		name      string
		record    elementRecord
		options   searchOptions
		wantField string
		wantKind  textMatchKind
	}{
		{
			name: "description exact match is used when title is empty",
			record: elementRecord{
				role:    "AXButton",
				desc:    "Mesh",
				w:       80,
				h:       24,
				enabled: true,
			},
			options:   searchOptions{Contains: "mesh"},
			wantField: "desc",
			wantKind:  matchExact,
		},
		{
			name: "value participates in exact title lookups",
			record: elementRecord{
				role:    "AXStaticText",
				value:   "Connected",
				w:       80,
				h:       24,
				enabled: true,
			},
			options:   searchOptions{Title: "connected"},
			wantField: "value",
			wantKind:  matchExact,
		},
		{
			name: "identifier match is case-insensitive",
			record: elementRecord{
				role:       "AXButton",
				identifier: "sidebar.mesh",
				w:          80,
				h:          24,
				enabled:    true,
			},
			options:   searchOptions{Contains: "SIDEBAR.MESH"},
			wantField: "identifier",
			wantKind:  matchExact,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, ok := matchElement(elementSnapshot{record: tt.record}, tt.options)
			if !ok {
				t.Fatalf("matchElement(%+v, %+v) = no match", tt.record, tt.options)
			}
			if match.fieldName != tt.wantField {
				t.Fatalf("match field = %q, want %q", match.fieldName, tt.wantField)
			}
			if match.matchKind != tt.wantKind {
				t.Fatalf("match kind = %v, want %v", match.matchKind, tt.wantKind)
			}
		})
	}
}

func TestCompareMatchesOrdering(t *testing.T) {
	contains := matchedElement{
		snapshot: elementSnapshot{record: elementRecord{
			role:       "AXButton",
			desc:       "Mesh Sidebar",
			enabled:    true,
			visible:    true,
			actionable: true,
			depth:      4,
			index:      1,
		}},
		fieldName:     "desc",
		fieldPriority: 1,
		matchKind:     matchContains,
	}
	exactDisabled := matchedElement{
		snapshot: elementSnapshot{record: elementRecord{
			role:       "AXButton",
			desc:       "Mesh",
			enabled:    false,
			visible:    true,
			actionable: true,
			depth:      5,
			index:      0,
		}},
		fieldName:     "desc",
		fieldPriority: 1,
		matchKind:     matchExact,
	}
	exactEnabled := matchedElement{
		snapshot: elementSnapshot{record: elementRecord{
			role:       "AXButton",
			desc:       "Mesh",
			enabled:    true,
			visible:    true,
			actionable: true,
			depth:      4,
			index:      2,
		}},
		fieldName:     "desc",
		fieldPriority: 1,
		matchKind:     matchExact,
	}

	matches := []matchedElement{contains, exactDisabled, exactEnabled}
	slices.SortStableFunc(matches, func(a, b matchedElement) int {
		switch {
		case compareMatches(a, b):
			return -1
		case compareMatches(b, a):
			return 1
		default:
			return 0
		}
	})

	if matches[0] != exactEnabled {
		t.Fatalf("best match = %+v, want exact enabled match", matches[0])
	}
	if matches[1] != contains {
		t.Fatalf("second match = %+v, want enabled visible substring match ahead of disabled exact match", matches[1])
	}
}

func TestCompareMatchesPrefersWebAreaOverSelectedBrowserTab(t *testing.T) {
	webArea := matchedElement{
		snapshot: elementSnapshot{record: elementRecord{
			role:       "AXWebArea",
			title:      "GitHub - ahl/apfs - GitHub",
			enabled:    true,
			visible:    true,
			actionable: false,
			depth:      6,
			index:      12,
		}},
		fieldName:     "title",
		fieldPriority: 0,
		matchKind:     matchExact,
	}
	selectedTab := matchedElement{
		snapshot: elementSnapshot{record: elementRecord{
			role:       "AXRadioButton",
			desc:       "GitHub - ahl/apfs - GitHub",
			value:      "1",
			enabled:    true,
			visible:    true,
			actionable: true,
			depth:      4,
			index:      3,
		}},
		fieldName:     "desc",
		fieldPriority: 1,
		matchKind:     matchExact,
	}

	matches := []matchedElement{selectedTab, webArea}
	slices.SortStableFunc(matches, func(a, b matchedElement) int {
		switch {
		case compareMatches(a, b):
			return -1
		case compareMatches(b, a):
			return 1
		default:
			return 0
		}
	})

	if matches[0] != webArea {
		t.Fatalf("best match = %+v, want AXWebArea", matches[0])
	}
}

func TestNoMatchMessageIncludesCandidates(t *testing.T) {
	result := matchResult{
		options: searchOptions{Contains: "mesh", Role: "AXButton"},
		candidates: []elementSnapshot{
			{record: elementRecord{
				role:       "AXButton",
				desc:       "Mesh",
				identifier: "sidebar.mesh",
				enabled:    true,
				visible:    true,
				actionable: true,
				x:          10,
				y:          20,
				w:          120,
				h:          28,
			}},
			{record: elementRecord{
				role:    "AXButton",
				title:   "Models",
				enabled: true,
				visible: true,
				x:       10,
				y:       60,
				w:       120,
				h:       28,
			}},
		},
	}

	msg := noMatchMessage(result)
	for _, want := range []string{
		`element containing "mesh", role "AXButton" not found`,
		`AXButton desc="Mesh" id="sidebar.mesh" bounds=(10,20 120x28)`,
		`bounds=(10,60 120x28)`,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("noMatchMessage missing %q in %q", want, msg)
		}
	}
}

func TestSearchTraversalLimitUsesTraversalFloor(t *testing.T) {
	if got := searchTraversalLimit(20); got != defaultSearchTraversalLimit {
		t.Fatalf("searchTraversalLimit(20) = %d, want %d", got, defaultSearchTraversalLimit)
	}
	if got := searchTraversalLimit(2500); got != 2500 {
		t.Fatalf("searchTraversalLimit(2500) = %d, want 2500", got)
	}
}

func TestMatchElementsFromSnapshotsRespectsResultLimit(t *testing.T) {
	snapshots := make([]elementSnapshot, 0, 12)
	for i := 0; i < 12; i++ {
		snapshots = append(snapshots, elementSnapshot{record: elementRecord{
			role:       "AXRadioButton",
			desc:       fmt.Sprintf("Tab %02d", i),
			enabled:    true,
			visible:    true,
			actionable: true,
			index:      i,
		}})
	}

	result := matchElementsFromSnapshots(snapshots, searchOptions{
		Role:     "AXRadioButton",
		Contains: "tab",
		Limit:    5,
	})
	if len(result.matches) != 5 {
		t.Fatalf("len(matches) = %d, want 5", len(result.matches))
	}
	if got := result.matches[4].snapshot.record.desc; got != "Tab 04" {
		t.Fatalf("fifth match = %q, want Tab 04", got)
	}
}

func TestMatchElementsFromSnapshotsFindsLaterMatches(t *testing.T) {
	snapshots := make([]elementSnapshot, 0, 30)
	for i := 0; i < 30; i++ {
		desc := fmt.Sprintf("Tab %02d", i)
		if i == 25 {
			desc = "Media"
		}
		snapshots = append(snapshots, elementSnapshot{record: elementRecord{
			role:       "AXRadioButton",
			desc:       desc,
			enabled:    true,
			visible:    true,
			actionable: true,
			index:      i,
		}})
	}

	result := matchElementsFromSnapshots(snapshots, searchOptions{
		Role:     "AXRadioButton",
		Contains: "Media",
		Limit:    20,
	})
	if len(result.matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(result.matches))
	}
	if got := result.matches[0].snapshot.record.desc; got != "Media" {
		t.Fatalf("match desc = %q, want Media", got)
	}
}

func TestResolveClickTargetFromDescendants(t *testing.T) {
	match := matchedElement{
		snapshot: elementSnapshot{record: elementRecord{
			role:       "AXGroup",
			desc:       "Mesh",
			enabled:    true,
			visible:    true,
			actionable: false,
		}},
		fieldName:     "desc",
		fieldPriority: 1,
		matchKind:     matchExact,
	}
	descendant := elementSnapshot{record: elementRecord{
		role:       "AXButton",
		desc:       "Mesh",
		enabled:    true,
		visible:    true,
		actionable: true,
	}}

	resolution := resolveClickTargetFromDescendants(match, []elementSnapshot{descendant})
	if !resolution.viaDescendant {
		t.Fatal("expected descendant resolution")
	}
	if resolution.target.record.role != "AXButton" {
		t.Fatalf("target role = %q, want AXButton", resolution.target.record.role)
	}
	if !strings.Contains(resolution.reason, "single actionable descendant") {
		t.Fatalf("reason = %q, want descendant explanation", resolution.reason)
	}
}
