//go:build darwin

// rapport-probe: investigate the Rapport.framework private API surface without
// loading or driving any actual session. Reports which classes are reachable,
// which selectors resolve, and what minimal instantiation looks like.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/axmcp/internal/purego/objc"
)

const rapportPath = "/System/Library/PrivateFrameworks/Rapport.framework/Versions/A/Rapport"

// Classes we want to probe. Ordered roughly: discovery -> session -> HID/text.
var probeClasses = []string{
	"RPCompanionLinkClient",
	"RPCompanionLinkDevice",
	"RPRemoteDisplayDevice",
	"RPRemoteDisplayDiscovery",
	"RPRemoteDisplaySession",
	"RPRemoteDisplayServer",
	"RPHIDSession",
	"RPHIDTouchSession",
	"RPHIDTouchEvent",
	"RPTextInputSession",
	"RPMediaControlSession",
	"RPSession",
	"RPConnection",
	"RPClient",
	"RPEndpoint",
}

// Runtime helpers we call beyond what objc.go exposes.
var (
	libobjc                uintptr
	class_copyMethodList   func(objc.Class, *uint32) unsafe.Pointer
	method_getName         func(uintptr) objc.SEL
	method_getTypeEncoding func(uintptr) *byte
	sel_getName            func(objc.SEL) *byte
	free                   func(unsafe.Pointer)
)

func initRuntime() {
	var err error
	libobjc, err = purego.Dlopen("libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: dlopen libobjc: %v\n", err)
		os.Exit(1)
	}
	purego.RegisterLibFunc(&class_copyMethodList, libobjc, "class_copyMethodList")
	purego.RegisterLibFunc(&method_getName, libobjc, "method_getName")
	purego.RegisterLibFunc(&method_getTypeEncoding, libobjc, "method_getTypeEncoding")
	purego.RegisterLibFunc(&sel_getName, libobjc, "sel_getName")
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err == nil {
		purego.RegisterLibFunc(&free, libc, "free")
	}
}

func cstring(p *byte) string {
	if p == nil {
		return ""
	}
	ptr := unsafe.Pointer(p)
	n := 0
	for *(*byte)(unsafe.Add(ptr, n)) != 0 {
		n++
	}
	return string(unsafe.Slice(p, n))
}

// dumpInstanceMethods returns sorted "name TYPES" strings for the class's
// instance methods. Filters by prefix if given.
func dumpInstanceMethods(cls objc.Class, prefixFilter string) []string {
	var n uint32
	list := class_copyMethodList(cls, &n)
	if list == nil || n == 0 {
		return nil
	}
	out := make([]string, 0, int(n))
	methods := unsafe.Slice((*uintptr)(list), int(n))
	for _, methPtr := range methods {
		sel := method_getName(methPtr)
		types := method_getTypeEncoding(methPtr)
		name := cstring((*byte)(unsafe.Pointer(sel_getName(sel))))
		if prefixFilter != "" && !strings.Contains(name, prefixFilter) {
			continue
		}
		out = append(out, fmt.Sprintf("%s    %s", name, cstring(types)))
	}
	if free != nil {
		free(list)
	}
	sort.Strings(out)
	return out
}

func main() {
	initRuntime()

	fmt.Println("rapport-probe: investigating /System/Library/PrivateFrameworks/Rapport.framework")
	fmt.Println()

	if _, err := purego.Dlopen(rapportPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: dlopen %s: %v\n", rapportPath, err)
		os.Exit(1)
	}

	classes := make(map[string]objc.Class, len(probeClasses))
	for _, name := range probeClasses {
		classes[name] = objc.GetClass(name)
	}

	// --- Section A: enumerate ALL real instance methods on the high-value
	// classes. This replaces guessing selector names with ground truth.
	fmt.Println("== real instance methods (filter applied) ==")
	for _, target := range []string{
		"RPCompanionLinkClient",
		"RPRemoteDisplaySession",
		"RPRemoteDisplayDiscovery",
		"RPHIDTouchSession",
		"RPTextInputSession",
	} {
		cls := classes[target]
		if cls == 0 {
			fmt.Printf("\n--- %s : NOT REGISTERED\n", target)
			continue
		}
		fmt.Printf("\n--- %s\n", target)
		methods := dumpInstanceMethods(cls, "")
		// Print first 80, then any "interesting" ones containing
		// substrings we care about (Handler/Block/Found/Device/Activate/Discovery/Send/Touch/start/invalidate/messenger).
		shown := 0
		for _, m := range methods {
			low := strings.ToLower(m)
			if strings.HasPrefix(low, "set") || strings.Contains(low, "handler") ||
				strings.Contains(low, "device") || strings.Contains(low, "activate") ||
				strings.Contains(low, "discover") || strings.Contains(low, "found") ||
				strings.Contains(low, "send") || strings.Contains(low, "messenger") ||
				strings.Contains(low, "touch") || strings.Contains(low, "screen") ||
				strings.Contains(low, "invalidate") || strings.Contains(low, "start") ||
				strings.Contains(low, "stop") {
				fmt.Printf("    %s\n", m)
				shown++
			}
		}
		fmt.Printf("    [%d filtered / %d total]\n", shown, len(methods))
	}

	// --- Section B: try the ACTIVATE flow on RPCompanionLinkClient using
	// only well-known selectors that we'll discover from the dump above.
	// This block is conditional on whether a "deviceFoundHandler"-shaped
	// setter exists; we look it up by introspection.
	fmt.Println("\n== attempt RPCompanionLinkClient discovery ==")
	clientCls := classes["RPCompanionLinkClient"]
	if clientCls == 0 {
		fmt.Println("  RPCompanionLinkClient not found, skipping")
		return
	}

	// Build the client.
	client := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(clientCls), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	if client == 0 {
		fmt.Println("  init returned nil")
		return
	}

	// Install handlers using REAL selector names confirmed by introspection above.
	deviceCount := 0
	foundHandler := objc.NewBlock(func(_ objc.Block, dev objc.ID) {
		deviceCount++
		desc := objc.Send[objc.ID](dev, objc.Sel("description"))
		fmt.Printf("  [foundDevice #%d] %s\n", deviceCount, objc.GoString(desc))
	})
	defer foundHandler.Release()
	changedHandler := objc.NewBlock(func(_ objc.Block, dev objc.ID, changes uint32) {
		desc := objc.Send[objc.ID](dev, objc.Sel("description"))
		fmt.Printf("  [changedDevice changes=%#x] %s\n", changes, objc.GoString(desc))
	})
	defer changedHandler.Release()
	stateHandler := objc.NewBlock(func(_ objc.Block) {
		fmt.Println("  [stateUpdated]")
	})
	defer stateHandler.Release()
	completionHandler := objc.NewBlock(func(_ objc.Block, err objc.ID) {
		if err == 0 {
			fmt.Println("  [activate] completion: nil error")
		} else {
			desc := objc.Send[objc.ID](err, objc.Sel("description"))
			fmt.Printf("  [activate] completion error: %s\n", objc.GoString(desc))
		}
	})
	defer completionHandler.Release()

	objc.Send[uintptr](client, objc.Sel("setDeviceFoundHandler:"), foundHandler)
	objc.Send[uintptr](client, objc.Sel("setDeviceChangedHandler:"), changedHandler)
	objc.Send[uintptr](client, objc.Sel("setStateUpdatedHandler:"), stateHandler)

	// Activate. Some Apple discovery clients dispatch on the main run loop,
	// others honor a setDispatchQueue:. The handlers above will fire
	// asynchronously regardless of run-loop spinning if the client uses an
	// internal queue.
	objc.Send[uintptr](client, objc.Sel("activateWithCompletion:"), completionHandler)
	fmt.Println("  invoked -activateWithCompletion: ; sleeping 4s for callbacks...")
	time.Sleep(4 * time.Second)

	// After activation, query state synchronously.
	fmt.Printf("  callbacks fired: %d\n", deviceCount)
	active := objc.Send[objc.ID](client, objc.Sel("activeDevices"))
	if active == 0 {
		fmt.Println("  activeDevices: nil")
	} else {
		count := objc.Send[uint64](active, objc.Sel("count"))
		fmt.Printf("  activeDevices: count=%d\n", count)
		desc := objc.Send[objc.ID](active, objc.Sel("description"))
		fmt.Printf("  activeDevices description: %s\n", objc.GoString(desc))
	}
	local := objc.Send[objc.ID](client, objc.Sel("localDevice"))
	if local != 0 {
		desc := objc.Send[objc.ID](local, objc.Sel("description"))
		fmt.Printf("  localDevice: %s\n", objc.GoString(desc))
	} else {
		fmt.Println("  localDevice: nil")
	}
	fmt.Printf("  client final description: %s\n",
		objc.GoString(objc.Send[objc.ID](client, objc.Sel("description"))))

	// Also try the dedicated RPRemoteDisplayDiscovery class which is what
	// ScreenContinuityServices itself uses.
	fmt.Println("\n== attempt RPRemoteDisplayDiscovery ==")
	discCls := classes["RPRemoteDisplayDiscovery"]
	if discCls == 0 {
		fmt.Println("  class missing")
		return
	}
	disc := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(discCls), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	if disc == 0 {
		fmt.Println("  init nil")
		return
	}
	dCount := 0
	dFound := objc.NewBlock(func(_ objc.Block, dev objc.ID) {
		dCount++
		desc := objc.Send[objc.ID](dev, objc.Sel("description"))
		fmt.Printf("  [disc.foundDevice #%d] %s\n", dCount, objc.GoString(desc))
	})
	defer dFound.Release()
	dCompletion := objc.NewBlock(func(_ objc.Block, err objc.ID) {
		if err == 0 {
			fmt.Println("  [disc.activate] completion: nil error")
		} else {
			desc := objc.Send[objc.ID](err, objc.Sel("description"))
			fmt.Printf("  [disc.activate] completion error: %s\n", objc.GoString(desc))
		}
	})
	defer dCompletion.Release()
	objc.Send[uintptr](disc, objc.Sel("setDeviceFoundHandler:"), dFound)
	objc.Send[uintptr](disc, objc.Sel("activateWithCompletion:"), dCompletion)
	fmt.Println("  invoked -activateWithCompletion: ; sleeping 4s...")
	time.Sleep(4 * time.Second)
	fmt.Printf("  disc callbacks fired: %d\n", dCount)
	devs := objc.Send[objc.ID](disc, objc.Sel("discoveredDevices"))
	if devs != 0 {
		c := objc.Send[uint64](devs, objc.Sel("count"))
		fmt.Printf("  discoveredDevices count=%d\n", c)
		fmt.Printf("  discoveredDevices description: %s\n",
			objc.GoString(objc.Send[objc.ID](devs, objc.Sel("description"))))
	} else {
		fmt.Println("  discoveredDevices: nil")
	}

}
