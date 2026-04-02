package main

import "testing"

func TestPreferredClickPoint(t *testing.T) {
	tests := []struct {
		name   string
		record elementRecord
		wantX  int
		wantY  int
		wantOK bool
	}{
		{
			name:   "row uses left inset",
			record: elementRecord{role: "AXRow", w: 120, h: 28},
			wantX:  12,
			wantY:  14,
			wantOK: true,
		},
		{
			name:   "small row falls back to center x",
			record: elementRecord{role: "AXCell", w: 8, h: 10},
			wantX:  4,
			wantY:  5,
			wantOK: true,
		},
		{
			name:   "non row has no preferred point",
			record: elementRecord{role: "AXButton", w: 120, h: 28},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY, gotOK := preferredClickPoint(elementSnapshot{record: tt.record})
			if gotOK != tt.wantOK {
				t.Fatalf("preferredClickPoint ok = %v, want %v", gotOK, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Fatalf("preferredClickPoint = (%d,%d), want (%d,%d)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}

func TestCenterClickPoint(t *testing.T) {
	tests := []struct {
		name   string
		record elementRecord
		wantX  int
		wantY  int
		wantOK bool
	}{
		{
			name:   "regular element uses midpoint",
			record: elementRecord{w: 120, h: 28},
			wantX:  60,
			wantY:  14,
			wantOK: true,
		},
		{
			name:   "single pixel element stays in bounds",
			record: elementRecord{w: 1, h: 1},
			wantX:  0,
			wantY:  0,
			wantOK: true,
		},
		{
			name:   "zero width is invalid",
			record: elementRecord{w: 0, h: 20},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY, gotOK := centerClickPoint(elementSnapshot{record: tt.record})
			if gotOK != tt.wantOK {
				t.Fatalf("centerClickPoint ok = %v, want %v", gotOK, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Fatalf("centerClickPoint = (%d,%d), want (%d,%d)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}

func TestPrefersAXPress(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{role: "AXButton", want: true},
		{role: "AXMenuItem", want: true},
		{role: "AXRadioButton", want: false},
		{role: "AXTextField", want: false},
		{role: "AXSearchField", want: false},
		{role: "AXRow", want: false},
	}

	for _, tt := range tests {
		if got := prefersAXPress(tt.role); got != tt.want {
			t.Fatalf("prefersAXPress(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestOCRToolCallFormatting(t *testing.T) {
	if got := ocrToolCall("workbench-ui", "Surface Workbench", "Planner"); got != `ax_ocr(app="workbench-ui", window="Surface Workbench", find="Planner")` {
		t.Fatalf("ocrToolCall = %q", got)
	}
	if got := ocrClickToolCall("workbench-ui", "", "Planner"); got != `ax_ocr_click(app="workbench-ui", find="Planner")` {
		t.Fatalf("ocrClickToolCall = %q", got)
	}
}

func TestWindowPointResultFormatting(t *testing.T) {
	if got := windowPointResult("clicked", `window "Surface Workbench"`, 12, 20); got != `clicked window "Surface Workbench" at local 12,20` {
		t.Fatalf("windowPointResult(click) = %q", got)
	}
	if got := windowPointResult("hovered", `window "Surface Workbench"`, 12, 20); got != `hovered window "Surface Workbench" at local 12,20` {
		t.Fatalf("windowPointResult(hover) = %q", got)
	}
}
