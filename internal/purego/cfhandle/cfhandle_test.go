package cfhandle

import (
	"github.com/ebitengine/purego"
	"testing"
)

func TestForeignObjects(t *testing.T) {
	lib, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Open()
	if err != nil || again != lib {
		t.Fatal("Open did not reuse library")
	}
	var create func(uintptr, string, uint32) uintptr
	if err := bind(lib.handle, "CFStringCreateWithCString", &create); err != nil {
		t.Fatal(err)
	}
	a := create(0, "native handle fixture alpha", 0x08000100)
	b := create(0, "native handle fixture alpha", 0x08000100)
	c := create(0, "native handle fixture beta", 0x08000100)
	if a == 0 || b == 0 || c == 0 {
		t.Fatal("create CFStrings")
	}
	defer lib.Release(a)
	defer lib.Release(b)
	defer lib.Release(c)
	for _, tt := range []struct {
		name string
		a, b uintptr
		want bool
	}{
		{"same", a, a, true}, {"equal objects", a, b, true}, {"different objects", a, c, false}, {"zero left", 0, a, false}, {"zero right", a, 0, false}, {"zeros", 0, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := lib.Equal(tt.a, tt.b); got != tt.want {
				t.Fatalf("Equal=%v want %v", got, tt.want)
			}
		})
	}
	lib.Retain(a)
	lib.Release(a)
	if !lib.Equal(a, b) {
		t.Fatal("balanced retain/release lost object")
	}
	lib.Retain(0)
	lib.Release(0)
	var missing func()
	if err := bind(lib.handle, "CFMissingSymbolForAdapterTest", &missing); err == nil {
		t.Fatal("missing symbol accepted")
	}
}

func TestOpenFailure(t *testing.T) {
	if lib, err := open("/nonexistent/cfhandle-test.framework"); err == nil || lib != nil {
		t.Fatalf("open=%v %v", lib, err)
	}
}

func TestOpenPrivate(t *testing.T) {
	lib, err := open(framework)
	if err != nil {
		t.Fatal(err)
	}
	if err := purego.Dlclose(lib.handle); err != nil {
		t.Fatal(err)
	}
}
