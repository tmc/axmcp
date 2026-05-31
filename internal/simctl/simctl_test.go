package simctl

import (
	"context"
	"testing"
)

func TestGetAppContainer(t *testing.T) {
	ctx := context.Background()
	sims, err := List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	bootedUDID := ""
	for _, s := range sims {
		if s.State == StateBooted {
			bootedUDID = s.UDID
			break
		}
	}

	if bootedUDID == "" {
		t.Skip("No booted simulator found, skipping GetAppContainer test")
	}

	path, err := GetAppContainer(ctx, bootedUDID, "com.apple.mobilesafari", "app")
	if err != nil {
		t.Logf("GetAppContainer failed for MobileSafari: %v", err)
	} else {
		t.Logf("MobileSafari App Path: %s", path)
		if path == "" {
			t.Error("Returned empty path")
		}
	}

	path, err = GetAppContainer(ctx, bootedUDID, "com.apple.mobilesafari", "data")
	if err != nil {
		t.Logf("GetAppContainer data failed: %v", err)
	} else {
		t.Logf("MobileSafari Data Path: %s", path)
	}
}
