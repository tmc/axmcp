package appstate

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestExactWindowIndex(t *testing.T) {
	for _, test := range []struct {
		name  string
		ids   []uint32
		want  uint32
		index int
		bad   bool
	}{
		{"exact second", []uint32{4, 9, 12}, 9, 1, false},
		{"absent never falls back", []uint32{4, 9}, 12, -1, true},
		{"zero identity", []uint32{0, 4}, 0, -1, true},
		{"duplicate identity", []uint32{4, 9, 9}, 9, -1, true},
		{"empty", nil, 9, -1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			index, err := exactWindowIndex(test.ids, test.want)
			if (err != nil) != test.bad || index != test.index {
				t.Fatalf("selection=%d,%v want %d bad=%v", index, err, test.index, test.bad)
			}
		})
	}
}

func TestBuildWindowValidation(t *testing.T) {
	b := NewBuilder()
	if _, err := b.BuildWindow(t.Context(), 0, 1, nil); err == nil {
		t.Fatal("zero PID accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := b.BuildWindow(ctx, 123, 1, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture=%v", err)
	}
}

func ExampleBuilder_BuildWindow() {
	builder := NewBuilder()
	_, err := builder.BuildWindow(context.Background(), 0, 0, nil)
	fmt.Println(err)
	// Output: pid must be positive
}
