//go:build darwin

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCalculatorClickSmoke(t *testing.T) {
	if os.Getenv("CALC_CLICK_TEST_SMOKE") == "" {
		t.Skip("set CALC_CLICK_TEST_SMOKE=1 to drive Calculator")
	}
	if os.Getenv("CI") != "" {
		t.Skip("headed Calculator smoke test is not for CI")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ".",
		"-sequence", "Clear,1,Add,2,Equals",
		"-repeat", "1",
		"-slow", "160ms",
		"-idle-wait", "80ms",
		"-end-wait", "120ms",
	)
	cmd.Env = append(os.Environ(), "MACGO_NO_RELAUNCH=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("calc-click-test timed out: %s", out)
	}
	if err != nil {
		t.Fatalf("calc-click-test: %v\n%s", err, out)
	}

	got := string(out)
	for _, want := range []string{
		`clicked "Clear"`,
		`clicked "1"`,
		`clicked "Add"`,
		`clicked "2"`,
		`clicked "Equals"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("calc-click-test output missing %q:\n%s", want, got)
		}
	}
}
