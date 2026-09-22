//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package approval

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthorityLockChild(t *testing.T) {
	path := os.Getenv("AXMCP_APPROVAL_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	f, err := lockAuthority(t.Context(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("locked")
	_, err = io.Copy(io.Discard, os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityLockAcrossProcesses(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "approvals.json")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthorityLockChild$")
	cmd.Env = append(os.Environ(), "AXMCP_APPROVAL_LOCK_TEST_PATH="+path)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		in.Close()
		if !waited {
			cancel()
			cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		t.Fatalf("child ready: %q, %v", line, err)
	}
	for _, exclusive := range []bool{false, true} {
		waitCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		f, err := lockAuthority(waitCtx, path, exclusive)
		stop()
		if f != nil {
			f.Close()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("exclusive=%v: got %v, want deadline exceeded", exclusive, err)
		}
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	f, err := lockAuthority(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("sidecar must survive release: %v", err)
	}
}
