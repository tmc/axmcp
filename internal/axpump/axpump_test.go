//go:build darwin

package axpump

import "testing"

func resetStateForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		mu.Lock()
		defer mu.Unlock()
		asserted = make(map[int32]bool)
		nonAssertable = make(map[int32]bool)
		observers = make(map[int32]*observerHold)
	}
	reset()
	t.Cleanup(reset)
}

func TestEnsureRejectsInvalidPID(t *testing.T) {
	resetStateForTest(t)
	for _, pid := range []int32{0, -1} {
		ok, err := Ensure(pid)
		if err == nil {
			t.Fatalf("Ensure(%d) error = nil, want invalid pid error", pid)
		}
		if ok {
			t.Fatalf("Ensure(%d) ok = true, want false", pid)
		}
	}
}

func TestEnsureUsesNonAssertableCache(t *testing.T) {
	resetStateForTest(t)
	const pid int32 = 12345
	nonAssertable[pid] = true

	ok, err := Ensure(pid)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if ok {
		t.Fatal("Ensure ok = true, want false for nonassertable cache")
	}
}

func TestEnsureUsesObserverCache(t *testing.T) {
	resetStateForTest(t)
	const pid int32 = 12345
	observers[pid] = &observerHold{}

	ok, err := Ensure(pid)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !ok {
		t.Fatal("Ensure ok = false, want true for observer cache")
	}
}
