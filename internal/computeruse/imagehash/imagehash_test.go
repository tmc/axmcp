package imagehash

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestDHash8(t *testing.T) {
	tests := []struct {
		name string
		img  image.Image
		want uint64
	}{
		{
			name: "increasing",
			img:  gradientImage(false),
			want: 0,
		},
		{
			name: "decreasing",
			img:  gradientImage(true),
			want: ^uint64(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := png.Encode(&buf, tt.img); err != nil {
				t.Fatalf("encode png: %v", err)
			}
			got, err := DHash8(buf.Bytes())
			if err != nil {
				t.Fatalf("DHash8: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DHash8=%016x, want %016x", got, tt.want)
			}
		})
	}
}

func TestDHash8RejectsInvalidPNG(t *testing.T) {
	if _, err := DHash8([]byte("not png")); err == nil {
		t.Fatal("DHash8 succeeded on invalid PNG")
	}
}

func gradientImage(decreasing bool) image.Image {
	img := image.NewGray(image.Rect(0, 0, 9, 8))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			v := uint8(x * 20)
			if decreasing {
				v = 255 - v
			}
			img.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return img
}
