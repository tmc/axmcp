//go:build darwin

package spacedetect

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
)

// ErrSkyLightUnavailable is returned when SkyLight cannot be loaded or one of
// the SLS symbols cannot be resolved on this system. Callers should treat it
// as "feature not available, continue without off-Space metadata".
var ErrSkyLightUnavailable = errors.New("spacedetect: SkyLight unavailable")

const skyLightPath = "/System/Library/PrivateFrameworks/SkyLight.framework/SkyLight"

// kCGSAllSpacesMask asks SLSCopySpacesForWindows for every Space a window
// belongs to (current, other-user, fullscreen, tiled). Matches yabai's
// convention; cua-driver uses the same value in SpaceMigrator.
const kCGSAllSpacesMask = 7

var (
	loadOnce sync.Once
	loadErr  error

	slsMainConnectionID    func() int32
	slsGetActiveSpace      func(conn int32) uint64
	slsCopySpacesForWindow func(conn int32, mask int32, wids corefoundation.CFArrayRef) corefoundation.CFArrayRef
)

func load() error {
	loadOnce.Do(func() {
		handle, err := purego.Dlopen(skyLightPath, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil || handle == 0 {
			loadErr = fmt.Errorf("%w: dlopen: %v", ErrSkyLightUnavailable, err)
			return
		}
		if err := registerLib(&slsMainConnectionID, handle, "SLSMainConnectionID"); err != nil {
			loadErr = err
			return
		}
		if err := registerLib(&slsGetActiveSpace, handle, "SLSGetActiveSpace"); err != nil {
			loadErr = err
			return
		}
		if err := registerLib(&slsCopySpacesForWindow, handle, "SLSCopySpacesForWindows"); err != nil {
			loadErr = err
			return
		}
	})
	return loadErr
}

func registerLib(fptr any, handle uintptr, name string) (err error) {
	sym, derr := purego.Dlsym(handle, name)
	if derr != nil || sym == 0 {
		return fmt.Errorf("%w: dlsym %s: %v", ErrSkyLightUnavailable, name, derr)
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: register %s: %v", ErrSkyLightUnavailable, name, r)
		}
	}()
	purego.RegisterFunc(fptr, sym)
	return nil
}

// IsOffSpace reports whether the given CGWindowID is on a Space other than
// the active Space. It returns (false, nil) for windows on the active Space,
// (true, nil) for windows on a different Space, and (false, err) when SkyLight
// cannot be reached or the lookup yields no Space membership for the window
// (which can happen for transient or system windows).
func IsOffSpace(windowID uint32) (bool, error) {
	if err := load(); err != nil {
		return false, err
	}

	conn := slsMainConnectionID()
	active := slsGetActiveSpace(conn)
	if active == 0 {
		return false, fmt.Errorf("spacedetect: SLSGetActiveSpace returned 0")
	}

	// Decode through int64 / KCFNumberSInt64Type so the full uint32
	// CGWindowID range survives without int32 truncation.
	wid := int64(windowID)
	wnum := corefoundation.CFNumberCreate(corefoundation.KCFAllocatorDefault, corefoundation.KCFNumberSInt64Type, unsafe.Pointer(&wid))
	if wnum == 0 {
		return false, fmt.Errorf("spacedetect: CFNumberCreate(windowID) returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(wnum))

	values := [1]uintptr{uintptr(wnum)}
	wids := corefoundation.CFArrayCreate(corefoundation.KCFAllocatorDefault, unsafe.Pointer(&values[0]), 1, &corefoundation.KCFTypeArrayCallBacks)
	if wids == 0 {
		return false, fmt.Errorf("spacedetect: CFArrayCreate(windowIDs) returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(wids))

	spaces := slsCopySpacesForWindow(conn, kCGSAllSpacesMask, wids)
	if spaces == 0 {
		return false, fmt.Errorf("spacedetect: SLSCopySpacesForWindows returned nil")
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(spaces))

	count := corefoundation.CFArrayGetCount(spaces)
	if count == 0 {
		return false, fmt.Errorf("spacedetect: window %d has no Space membership", windowID)
	}
	parsedAny := false
	for i := range count {
		nptr := corefoundation.CFArrayGetValueAtIndex(spaces, i)
		num := corefoundation.CFNumberRef(uintptr(nptr))
		var sid int64
		if !corefoundation.CFNumberGetValue(num, corefoundation.KCFNumberSInt64Type, unsafe.Pointer(&sid)) {
			continue
		}
		parsedAny = true
		if uint64(sid) == active {
			return false, nil
		}
	}
	if !parsedAny {
		return false, fmt.Errorf("spacedetect: failed to decode any Space IDs for window %d", windowID)
	}
	return true, nil
}
