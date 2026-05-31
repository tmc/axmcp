package computeruse

import "testing"

func TestScaledSize(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		height      int
		maxLongSide int
		wantWidth   int
		wantHeight  int
	}{
		{"wide", 3136, 1960, 1568, 1568, 980},
		{"tall", 1960, 3136, 1568, 980, 1568},
		{"rounds to at least one pixel", 10000, 1, 1568, 1568, 1},
		{"zero width", 0, 100, 1568, 0, 100},
		{"zero max", 100, 50, 0, 100, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWidth, gotHeight := ScaledSize(tt.width, tt.height, tt.maxLongSide)
			if gotWidth != tt.wantWidth || gotHeight != tt.wantHeight {
				t.Fatalf("ScaledSize(%d, %d, %d) = %d, %d, want %d, %d", tt.width, tt.height, tt.maxLongSide, gotWidth, gotHeight, tt.wantWidth, tt.wantHeight)
			}
		})
	}
}
