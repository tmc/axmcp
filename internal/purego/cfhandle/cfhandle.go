package cfhandle

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

const framework = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"

// Library contains bound CoreFoundation operations. Its zero value is not usable.
// Obtain a Library with Open. Methods may be called concurrently when the caller
// independently guarantees that each referenced OS object remains alive.
type Library struct {
	handle  uintptr
	equal   func(uintptr, uintptr) bool
	retain  func(uintptr) uintptr
	release func(uintptr)
}

var load = sync.OnceValues(func() (*Library, error) { return open(framework) })

// Open returns the shared CoreFoundation library or its initialization error.
// The framework remains loaded for the process lifetime so deferred cleanup can
// always call the bound functions. Initialization failures are cached.
func Open() (*Library, error) { return load() }

func open(path string) (*Library, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("open CoreFoundation: %w", err)
	}
	lib := &Library{handle: handle}
	for _, binding := range []struct {
		name string
		fn   any
	}{
		{"CFEqual", &lib.equal}, {"CFRetain", &lib.retain}, {"CFRelease", &lib.release},
	} {
		if err := bind(handle, binding.name, binding.fn); err != nil {
			_ = purego.Dlclose(handle)
			return nil, err
		}
	}
	return lib, nil
}

func bind(handle uintptr, name string, fn any) error {
	symbol, err := purego.Dlsym(handle, name)
	if err != nil {
		return fmt.Errorf("bind %s: %w", name, err)
	}
	purego.RegisterFunc(fn, symbol)
	return nil
}

// Equal reports CoreFoundation equality. It returns false if either handle is zero.
func (l *Library) Equal(a, b uintptr) bool {
	return a != 0 && b != 0 && l.equal(a, b)
}

// Retain acquires one ownership reference. A zero handle is ignored.
func (l *Library) Retain(ref uintptr) {
	if ref != 0 {
		l.retain(ref)
	}
}

// Release gives up one ownership reference. A zero handle is ignored.
func (l *Library) Release(ref uintptr) {
	if ref != 0 {
		l.release(ref)
	}
}
