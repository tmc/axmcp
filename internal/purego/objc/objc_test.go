package objc

import "testing"

func TestMethodIMPRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		impl any
	}{
		{name: "nil", impl: nil},
		{name: "function", impl: func() {}},
		{name: "zero uintptr", impl: uintptr(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := methodIMP(tt.impl); err == nil {
				t.Fatal("methodIMP returned nil error")
			}
		})
	}
}

func TestMethodIMPAcceptsUintptr(t *testing.T) {
	imp, err := methodIMP(uintptr(1))
	if err != nil {
		t.Fatalf("methodIMP: %v", err)
	}
	if imp != 1 {
		t.Fatalf("methodIMP = %d, want 1", imp)
	}
}

func TestAddMethodCheckedRejectsInvalidIMPBeforeRuntime(t *testing.T) {
	if ok, err := AddMethodChecked(0, 0, func() {}, "v@:"); err == nil || ok {
		t.Fatalf("AddMethodChecked = %v, %v; want false and error", ok, err)
	}
}
