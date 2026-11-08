//go:build ignore

// Значки Little Hub: go run tools/icons/main.go. Синий скруглённый квадрат с белым кольцом и точкой («хаб»);
// для строки меню Mac — то же чёрным на прозрачном (template). Windows — ICO из BMP 16 и 32 (LoadImage их точно читает).
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func shape(n int, bg, fg color.RGBA, withBg bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	s := float64(n)
	r := s * 0.22 // скругление
	c := s / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			// 4×4 подвыборки — гладкие края.
			var cov, ring float64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px := float64(x) + (float64(sx)+0.5)/4
					py := float64(y) + (float64(sy)+0.5)/4
					dx := math.Max(math.Max(r-px, px-(s-r)), 0)
					dy := math.Max(math.Max(r-py, py-(s-r)), 0)
					if dx*dx+dy*dy <= r*r {
						cov++
					}
					d := math.Hypot(px-c, py-c)
					if (d >= s*0.24 && d <= s*0.34) || d <= s*0.1 {
						ring++
					}
				}
			}
			cov /= 16
			ring /= 16
			if withBg {
				a := cov
				col := blend(bg, fg, ring)
				img.SetRGBA(x, y, color.RGBA{uint8(float64(col.R) * a), uint8(float64(col.G) * a), uint8(float64(col.B) * a), uint8(255 * a)})
			} else {
				a := ring
				img.SetRGBA(x, y, color.RGBA{uint8(float64(fg.R) * a), uint8(float64(fg.G) * a), uint8(float64(fg.B) * a), uint8(255 * a)})
			}
		}
	}
	return img
}

func blend(a, b color.RGBA, t float64) color.RGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return color.RGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 255}
}

func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// dib — картинка ICO в формате BMP: BITMAPINFOHEADER, 32 бита BGRA снизу вверх, маска AND.
func dib(img *image.RGBA) []byte {
	n := img.Bounds().Dx()
	var b bytes.Buffer
	h := struct {
		Size                         uint32
		W, H                         int32
		Planes, Bits                 uint16
		Comp, ImgSize, XPM, YPM, Clr uint32
		Imp                          uint32
	}{40, int32(n), int32(n * 2), 1, 32, 0, 0, 0, 0, 0, 0}
	_ = binary.Write(&b, binary.LittleEndian, h)
	for y := n - 1; y >= 0; y-- {
		for x := 0; x < n; x++ {
			c := img.RGBAAt(x, y)
			// В BMP цвет не умножен на прозрачность.
			un := func(v uint8) uint8 {
				if c.A == 0 {
					return 0
				}
				return uint8(math.Min(255, float64(v)*255/float64(c.A)))
			}
			b.Write([]byte{un(c.B), un(c.G), un(c.R), c.A})
		}
	}
	row := ((n + 31) / 32) * 4
	b.Write(make([]byte, row*n))
	return b.Bytes()
}

func ico(imgs ...*image.RGBA) []byte {
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(imgs))})
	off := 6 + 16*len(imgs)
	var data [][]byte
	for _, im := range imgs {
		d := dib(im)
		n := im.Bounds().Dx()
		_ = binary.Write(&out, binary.LittleEndian, struct {
			W, H, C, R uint8
			P, B       uint16
			Size, Off  uint32
		}{uint8(n), uint8(n), 0, 0, 1, 32, uint32(len(d)), uint32(off)})
		off += len(d)
		data = append(data, d)
	}
	for _, d := range data {
		out.Write(d)
	}
	return out.Bytes()
}

func main() {
	blue := color.RGBA{0x0a, 0x66, 0xd8, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	must(os.WriteFile("internal/ui/web/icon.png", pngBytes(shape(128, blue, white, true)), 0o644))
	must(os.WriteFile("internal/tray/icon.png", pngBytes(shape(64, blue, white, true)), 0o644))
	must(os.WriteFile("internal/tray/template.png", pngBytes(shape(44, black, black, false)), 0o644))
	must(os.WriteFile("internal/tray/icon.ico", ico(shape(16, blue, white, true), shape(32, blue, white, true)), 0o644))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
