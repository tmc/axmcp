//go:build !darwin

// Package objc provides Objective-C runtime bindings on Darwin.
package objc

import "fmt"

type (
	ID       uintptr
	SEL      uintptr
	Class    uintptr
	Block    uintptr
	Protocol uintptr
)

func (b Block) Release() {}

// Sel returns zero on platforms without Objective-C.
func Sel(string) SEL {
	return 0
}

// GetClass returns zero on platforms without Objective-C.
func GetClass(string) Class {
	return 0
}

// Send returns the zero value on platforms without Objective-C.
func Send[T any](ID, SEL, ...any) T {
	var zero T
	return zero
}

// NewBlock returns zero on platforms without Objective-C.
func NewBlock(any) Block {
	return 0
}

// NSString returns zero on platforms without Objective-C.
func NSString(string) ID {
	return 0
}

// GoString returns an empty string on platforms without Objective-C.
func GoString(ID) string {
	return ""
}

// Dlopen reports that Objective-C dynamic loading is unavailable.
func Dlopen(string, int) (uintptr, error) {
	return 0, fmt.Errorf("objective-c runtime is not available on this platform")
}

const (
	RTLD_NOW    = 0
	RTLD_GLOBAL = 0
)

// AllocateClassPair returns zero on platforms without Objective-C.
func AllocateClassPair(Class, string, int) Class {
	return 0
}

// RegisterClassPair is a no-op on platforms without Objective-C.
func RegisterClassPair(Class) {}

// AddMethod adds a new method to a class. It returns false for an invalid IMP
// or unavailable runtime.
func AddMethod(cls Class, sel SEL, impl any, types string) bool {
	ok, err := AddMethodChecked(cls, sel, impl, types)
	return err == nil && ok
}

// AddMethodChecked reports invalid IMP values as errors and otherwise returns
// false on platforms without Objective-C.
func AddMethodChecked(_ Class, _ SEL, impl any, _ string) (bool, error) {
	_, err := methodIMP(impl)
	if err != nil {
		return false, err
	}
	return false, nil
}

func methodIMP(impl any) (uintptr, error) {
	switch v := impl.(type) {
	case uintptr:
		if v == 0 {
			return 0, fmt.Errorf("add method: nil IMP")
		}
		return v, nil
	default:
		return 0, fmt.Errorf("add method: impl must be uintptr IMP from NewCallback")
	}
}

// NewCallback returns zero on platforms without Objective-C.
func NewCallback(any) uintptr {
	return 0
}
