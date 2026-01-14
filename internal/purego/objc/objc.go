// Package objc provides Objective-C runtime bindings using purego.
package objc

import (
	"sync"
	"unsafe"

	basepurego "github.com/ebitengine/purego"
	purego "github.com/ebitengine/purego/objc"
)

// Type aliases from purego/objc
type (
	ID       = purego.ID
	SEL      = purego.SEL
	Class    = purego.Class
	Block    = purego.Block
	Protocol = purego.Protocol
)

var (
	selCache sync.Map // map[string]SEL
)

// Sel returns a cached selector for the given name.
func Sel(name string) SEL {
	if sel, ok := selCache.Load(name); ok {
		return sel.(SEL)
	}
	sel := purego.RegisterName(name)
	selCache.Store(name, sel)
	return sel
}

// GetClass returns the Objective-C class with the given name.
func GetClass(name string) Class {
	return purego.GetClass(name)
}

// Send calls objc_msgSend with the given arguments.
func Send[T any](id ID, sel SEL, args ...any) T {
	return purego.Send[T](id, sel, args...)
}

// NewBlock creates an Objective-C block from a Go function.
// The Go function must take a Block as its first argument.
// Use Block.Release() to free the block when done.
func NewBlock(fn any) Block {
	return purego.NewBlock(fn)
}

// NSString converts a Go string to an NSString object.
func NSString(s string) ID {
	cls := GetClass("NSString")
	return Send[ID](ID(cls), Sel("stringWithUTF8String:"), s)
}

// GoString converts an NSString to a Go string.
func GoString(nsstr ID) string {
	if nsstr == 0 {
		return ""
	}
	cstr := Send[*byte](nsstr, Sel("UTF8String"))
	return goStringFromCString(cstr)
}

func goStringFromCString(cstr *byte) string {
	if cstr == nil {
		return ""
	}
	ptr := unsafe.Pointer(cstr)
	length := 0
	for *(*byte)(unsafe.Add(ptr, length)) != 0 {
		length++
	}
	return string(unsafe.Slice(cstr, length))
}

// Dlopen loads a dynamic library.
func Dlopen(path string, mode int) (uintptr, error) {
	return basepurego.Dlopen(path, mode)
}

// RTLD constants for Dlopen
const (
	RTLD_NOW    = basepurego.RTLD_NOW
	RTLD_GLOBAL = basepurego.RTLD_GLOBAL
)

// RegisterName registers a selector with the Objective-C runtime.
func RegisterName(name string) SEL {
	return purego.RegisterName(name)
}

var (
	objcLib                uintptr
	objcOnce               sync.Once
	objc_registerClassPair func(Class)
	class_addMethod        func(Class, SEL, uintptr, string) bool
	objc_allocateClassPair func(Class, string, int) Class
)

func initObjCRuntime() {
	objcOnce.Do(func() {
		var err error
		objcLib, err = basepurego.Dlopen("libobjc.A.dylib", basepurego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		basepurego.RegisterLibFunc(&objc_registerClassPair, objcLib, "objc_registerClassPair")
		basepurego.RegisterLibFunc(&class_addMethod, objcLib, "class_addMethod")
		basepurego.RegisterLibFunc(&objc_allocateClassPair, objcLib, "objc_allocateClassPair")
	})
}

// AllocateClassPair calls objc_allocateClassPair.
func AllocateClassPair(super Class, name string, extraBytes int) Class {
	initObjCRuntime()
	if objc_allocateClassPair == nil {
		return 0
	}
	return objc_allocateClassPair(super, name, extraBytes)
}

// RegisterClassPair registers a class pair with the runtime.
func RegisterClassPair(cls Class) {
	initObjCRuntime()
	if objc_registerClassPair != nil {
		objc_registerClassPair(cls)
	}
}

// AddMethod adds a new method to a class.
// impl must be a uintptr from purego.NewCallback.
func AddMethod(cls Class, sel SEL, impl any, types string) bool {
	initObjCRuntime()
	if class_addMethod == nil {
		return false
	}

	var imp uintptr
	switch v := impl.(type) {
	case uintptr:
		imp = v
	default:
		panic("AddMethod expects uintptr IMP (use purego.NewCallback)")
	}
	return class_addMethod(cls, sel, imp, types)
}

// NewCallback creates a Go callback for C.
func NewCallback(fn any) uintptr {
	return basepurego.NewCallback(fn)
}
