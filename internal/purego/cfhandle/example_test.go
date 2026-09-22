package cfhandle_test

import (
	"fmt"
	"github.com/tmc/axmcp/internal/purego/cfhandle"
)

func Example() {
	lib, err := cfhandle.Open()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(lib.Equal(0, 0))
	// Output: false
}

func ExampleOpen() {
	lib, err := cfhandle.Open()
	fmt.Println(lib != nil, err == nil)
	// Output: true true
}

func ExampleLibrary_Equal() {
	lib, _ := cfhandle.Open()
	fmt.Println(lib.Equal(0, 0))
	// Output: false
}

func ExampleLibrary_Retain() {
	lib, _ := cfhandle.Open()
	lib.Retain(0)
	// Output:
}

func ExampleLibrary_Release() {
	lib, _ := cfhandle.Open()
	lib.Release(0)
	// Output:
}
