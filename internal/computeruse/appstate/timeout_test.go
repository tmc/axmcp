package appstate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tmc/apple/x/axuiautomation"
)

func TestAXGroupedCallBudget(t *testing.T) {
	original := axSetMessagingTimeout
	defer func() { axSetMessagingTimeout = original }()
	var timeouts []float32
	axSetMessagingTimeout = func(_ uintptr, seconds float32) int32 { timeouts = append(timeouts, seconds); return 0 }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := boundAXCalls(ctx, &axuiautomation.Element{}, 3); err != nil {
		t.Fatal(err)
	}
	if len(timeouts) != 1 || timeouts[0] <= 0 || timeouts[0] > 0.666667 {
		t.Fatalf("three-call timeout budget=%v", timeouts)
	}
	cancel()
	if err := boundAXCalls(ctx, &axuiautomation.Element{}, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled call=%v", err)
	}
	if len(timeouts) != 1 {
		t.Fatal("canceled request changed native timeout")
	}
	if err := boundAXCalls(context.Background(), &axuiautomation.Element{}, 3); err != nil {
		t.Fatal(err)
	}
	if timeouts[1] != axTimeout {
		t.Fatalf("unbounded context default=%v", timeouts[1])
	}
}
