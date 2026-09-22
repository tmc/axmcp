package main

import (
	"fmt"
	"math"

	"github.com/tmc/axmcp/internal/computeruse"
)

// nativePoint is expressed in raw PNG pixels on input. The mapper returns
// window-local and global macOS logical points without rounding or clamping.
type nativePoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func mapNativePoint(info *computeruse.ScreenshotInfo, point nativePoint) (local, global nativePoint, err error) {
	if info == nil || info.SourceKind != "window" || info.TargetWindow == 0 || info.ImageID == "" || info.Width <= 0 || info.Height <= 0 {
		return local, global, fmt.Errorf("screenshot mapping unavailable")
	}
	r := info.GlobalRect
	for _, v := range []float64{point.X, point.Y, r.X, r.Y, r.Width, r.Height, info.ScaleX, info.ScaleY} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return local, global, fmt.Errorf("screenshot coordinates must be finite")
		}
	}
	if r.Width <= 0 || r.Height <= 0 || info.ScaleX <= 0 || info.ScaleY <= 0 || info.ScaleX != float64(info.Width)/r.Width || info.ScaleY != float64(info.Height)/r.Height {
		return local, global, fmt.Errorf("inconsistent screenshot mapping")
	}
	if point.X < 0 || point.Y < 0 || point.X >= float64(info.Width) || point.Y >= float64(info.Height) {
		return local, global, fmt.Errorf("screenshot point out of bounds")
	}
	local = nativePoint{X: point.X / info.ScaleX, Y: point.Y / info.ScaleY}
	global = nativePoint{X: r.X + local.X, Y: r.Y + local.Y}
	for _, v := range []float64{local.X, local.Y, global.X, global.Y} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nativePoint{}, nativePoint{}, fmt.Errorf("screenshot coordinate overflow")
		}
	}
	return local, global, nil
}

func nativeHasCoordinates(in nativeActInput) bool {
	return in.Point != nil || in.FromPoint != nil || in.ToPoint != nil
}

func validateNativePointerInput(in nativeActInput) error {
	switch in.Action {
	case "click":
		if in.FromPoint != nil || in.ToPoint != nil || in.DurationMS != 0 {
			return fmt.Errorf("click does not accept drag parameters")
		}
		if in.Point != nil && in.ElementIndex != nil {
			return fmt.Errorf("click requires exactly one element_index or point")
		}
		if in.ClickCount < 0 || in.ClickCount > 3 {
			return fmt.Errorf("click_count must be from 1 through 3")
		}
		if in.Point == nil && (in.ImageID != "" || in.ClickCount > 1 || (in.MouseButton != "" && in.MouseButton != "left")) {
			return fmt.Errorf("mouse options require a screenshot point")
		}
	case "drag":
		if in.FromPoint == nil || in.ToPoint == nil || in.Point != nil || in.ElementIndex != nil || in.ClickCount != 0 {
			return fmt.Errorf("drag requires only from_point and to_point")
		}
		if in.DurationMS != 0 && (in.DurationMS < 100 || in.DurationMS > 5000) {
			return fmt.Errorf("drag duration_ms must be from 100 through 5000")
		}
	default:
		if nativeHasCoordinates(in) || in.ImageID != "" || in.MouseButton != "" || in.ClickCount != 0 || in.DurationMS != 0 {
			return fmt.Errorf("pointer parameters require click or drag")
		}
	}
	if in.MouseButton != "" && in.MouseButton != "left" && in.MouseButton != "right" && in.MouseButton != "middle" {
		return fmt.Errorf("invalid mouse_button")
	}
	if nativeHasCoordinates(in) && in.ImageID == "" {
		return fmt.Errorf("coordinate action requires image_id")
	}
	return nil
}

func validateNativePointerImage(in nativeActInput, state computeruse.AppState) error {
	if !nativeHasCoordinates(in) {
		return nil
	}
	info := state.ScreenshotMetadata
	if info == nil || info.ImageID != in.ImageID || info.TargetWindow != state.Window.WindowID {
		return fmt.Errorf("screenshot image_id or window mismatch")
	}
	for _, point := range []*nativePoint{in.Point, in.FromPoint, in.ToPoint} {
		if point != nil {
			if _, _, err := mapNativePoint(info, *point); err != nil {
				return err
			}
		}
	}
	return nil
}
