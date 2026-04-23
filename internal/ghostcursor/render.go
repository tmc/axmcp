package ghostcursor

import (
	"fmt"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/coregraphics"
	"github.com/tmc/apple/quartzcore"
)

// RenderColor describes an sRGB color for offscreen cursor rendering.
type RenderColor struct {
	Red   float64
	Green float64
	Blue  float64
	Alpha float64
}

// RenderOptions controls offscreen cursor rendering.
type RenderOptions struct {
	Size       int
	Theme      Theme
	Activity   ActivityState
	Background RenderColor
	DX         float64
	DY         float64
	Speed      float64
}

// RenderPNG rasterizes the current cursor style to a PNG.
func RenderPNG(opts RenderOptions) ([]byte, error) {
	opts = normalizeRenderOptions(opts)

	var (
		png []byte
		err error
	)
	runOnMain(func() {
		png, err = renderPNGOnMain(opts)
	})
	return png, err
}

func normalizeRenderOptions(opts RenderOptions) RenderOptions {
	if opts.Size <= 0 {
		opts.Size = int(windowSize * 2)
	}
	if opts.Theme == ThemeAuto {
		opts.Theme = ThemeCodex
	}
	return opts
}

func renderPNGOnMain(opts RenderOptions) ([]byte, error) {
	space := coregraphics.CGColorSpaceCreateDeviceRGB()
	if space == 0 {
		return nil, fmt.Errorf("create device rgb color space")
	}
	defer coregraphics.CGColorSpaceRelease(space)

	width := uintptr(opts.Size)
	height := uintptr(opts.Size)
	bytesPerRow := uintptr(opts.Size * 4)
	data := make([]byte, int(height*bytesPerRow))
	ctx := coregraphics.CGBitmapContextCreate(
		unsafe.Pointer(unsafe.SliceData(data)),
		width,
		height,
		8,
		bytesPerRow,
		space,
		coregraphics.CGBitmapInfo(coregraphics.KCGImageAlphaPremultipliedLast)|coregraphics.KCGBitmapByteOrder32Host,
	)
	if ctx == 0 {
		return nil, fmt.Errorf("create bitmap context")
	}
	defer coregraphics.CGContextRelease(ctx)

	if opts.Background.Alpha > 0 {
		coregraphics.CGContextSetRGBFillColor(
			ctx,
			opts.Background.Red,
			opts.Background.Green,
			opts.Background.Blue,
			opts.Background.Alpha,
		)
		coregraphics.CGContextFillRect(ctx, corefoundationRect(0, 0, float64(opts.Size), float64(opts.Size)))
	}

	controller := New(Config{
		Enabled: true,
		Theme:   opts.Theme,
		Eyecandy: EyecandyConfig{
			SharingVisible: true,
		},
	})
	root := quartzcore.NewCALayer()
	controller.installLayerTree(root)
	controller.applyActivity(opts.Activity)
	controller.applyMotionTransform(opts.Activity, opts.DX, opts.DY, opts.Speed)

	coregraphics.CGContextSaveGState(ctx)
	scale := float64(opts.Size) / windowSize
	coregraphics.CGContextScaleCTM(ctx, scale, scale)
	root.RenderInContext(ctx)
	coregraphics.CGContextRestoreGState(ctx)

	img := coregraphics.CGBitmapContextCreateImage(ctx)
	if img == 0 {
		return nil, fmt.Errorf("create image from bitmap context")
	}
	defer coregraphics.CGImageRelease(img)
	return cgImageToPNG(img)
}

func corefoundationRect(x, y, width, height float64) corefoundation.CGRect {
	return corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: x, Y: y},
		Size:   corefoundation.CGSize{Width: width, Height: height},
	}
}

func cgImageToPNG(img coregraphics.CGImageRef) ([]byte, error) {
	if img == 0 {
		return nil, fmt.Errorf("nil CGImage")
	}
	rep := appkit.NewBitmapImageRepWithCGImage(img)
	if rep.GetID() == 0 {
		return nil, fmt.Errorf("create bitmap image rep")
	}
	data := rep.RepresentationUsingTypeProperties(appkit.NSBitmapImageFileTypePNG, nil)
	if data == nil || data.Length() == 0 {
		return nil, fmt.Errorf("create png representation")
	}
	raw := unsafe.Slice((*byte)(data.Bytes()), data.Length())
	out := make([]byte, len(raw))
	copy(out, raw)
	return out, nil
}
