package macosapp

import "testing"

func TestParseLSAppInfoList(t *testing.T) {
	apps := ParseLSAppInfoList(`ASN:0x0-0x1) "Finder"
bundleID="com.apple.finder"
pid = 123
ASN:0x0-0x2) "Mesh"
bundleID="dev.tmc.Mesh"
pid = 456`)

	if len(apps) != 2 {
		t.Fatalf("len(apps) = %d, want 2", len(apps))
	}
	if apps[1].BundleID != "dev.tmc.Mesh" || apps[1].PID != 456 {
		t.Fatalf("apps[1] = %+v", apps[1])
	}
}

func TestFindRunningApp(t *testing.T) {
	apps := []RunningApp{
		{Name: "Finder", BundleID: "com.apple.finder", PID: 123},
		{Name: "Mesh", BundleID: "dev.tmc.Mesh", PID: 456},
	}
	if app := FindRunningApp(apps, AppSelector{BundleID: "dev.tmc.Mesh"}); app == nil || app.PID != 456 {
		t.Fatalf("FindRunningApp by bundle id = %+v", app)
	}
	if app := FindRunningApp(apps, AppSelector{PID: 123}); app == nil || app.Name != "Finder" {
		t.Fatalf("FindRunningApp by pid = %+v", app)
	}
}

func TestChooseReadyWindow(t *testing.T) {
	window, ok := ChooseReadyWindow([]WindowInfo{
		{Title: "", Width: 0, Height: 0, ChildCount: 0},
		{Title: "Mesh", Width: 900, Height: 700, ChildCount: 4},
	}, WaitOptions{
		RequireWindow:      true,
		RequireWindowTitle: true,
		RequireContent:     true,
	})
	if !ok {
		t.Fatal("ChooseReadyWindow returned no window")
	}
	if window.Title != "Mesh" {
		t.Fatalf("window.Title = %q, want Mesh", window.Title)
	}
}
