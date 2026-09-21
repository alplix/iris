//go:build ignore

// gen_icon renders the Iris application icon — the white iris flower of the
// UI's sidebar logo on the purple accent gradient — and writes every icon file
// the build needs:
//
//	build/appicon.png        1024x1024, source for macOS/Linux icons and the favicon
//	build/icon.png           256x256, tray icon on Linux
//	build/icon.ico           tray icon on Windows
//	build/windows/icon.ico   executable, taskbar and installer icon (16-256 px)
//	frontend/public/appicon.png
//
// Run from the repository root: go run build/gen_icon.go
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

// The flower is drawn on the same 24x24 grid as the SVG in frontend/src/main.js.
type ellipse struct{ cx, cy, rx, ry, deg float64 }

var petals = []ellipse{
	{12, 5.5, 2.2, 4, 0},
	{8.5, 7, 2, 3.5, -25},
	{15.5, 7, 2, 3.5, 25},
	{7, 10.5, 1.8, 3, -40},
	{17, 10.5, 1.8, 3, 40},
}

// leaves are the SVG's cubic curves (start, two controls, end), sampled into
// polylines once at start-up.
var leafCurves = [][8]float64{
	{12, 15, 10, 16.5, 8, 16, 7, 15},
	{12, 17.5, 13.8, 19, 15.5, 18.5, 16.5, 17.5},
}

var leaves = func() [][]float64 {
	var out [][]float64
	for _, c := range leafCurves {
		var pts []float64
		for i := 0; i <= 16; i++ {
			t := float64(i) / 16
			u := 1 - t
			x := u*u*u*c[0] + 3*u*u*t*c[2] + 3*u*t*t*c[4] + t*t*t*c[6]
			y := u*u*u*c[1] + 3*u*u*t*c[3] + 3*u*t*t*c[5] + t*t*t*c[7]
			pts = append(pts, x, y)
		}
		out = append(out, pts)
	}
	return out
}()

func inEllipse(e ellipse, x, y float64) bool {
	s, c := math.Sincos(e.deg * math.Pi / 180)
	dx, dy := x-e.cx, y-e.cy
	u := dx*c + dy*s
	v := -dx*s + dy*c
	return (u*u)/(e.rx*e.rx)+(v*v)/(e.ry*e.ry) <= 1
}

func distSeg(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

// glyph returns the flower's coverage (0..1) at a point of the 24x24 grid.
func glyph(x, y float64) float64 {
	for _, p := range petals {
		if inEllipse(p, x, y) {
			return 1
		}
	}
	// stem
	if distSeg(x, y, 12, 11, 12, 22) <= 0.9 {
		return 0.8
	}
	// leaves
	for _, l := range leaves {
		for i := 0; i+3 < len(l); i += 2 {
			if distSeg(x, y, l[i], l[i+1], l[i+2], l[i+3]) <= 0.6 {
				return 0.6
			}
		}
	}
	return 0
}

// roundedRect reports whether a point lies inside a rounded square.
func roundedRect(x, y, size, r float64) bool {
	cx := math.Max(r, math.Min(size-r, x))
	cy := math.Max(r, math.Min(size-r, y))
	return math.Hypot(x-cx, y-cy) <= r
}

func lerp(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }

// render draws the icon at size x size pixels with 4x4 supersampling.
func render(size int) *image.NRGBA {
	const ss = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	fs := float64(size)
	margin := fs * 0.04
	inner := fs - 2*margin
	radius := inner * 0.22
	top := color.NRGBA{124, 58, 237, 255}    // #7c3aed
	bottom := color.NRGBA{99, 102, 241, 255} // #6366f1
	// the flower fills ~62% of the tile, centred
	gs := inner * 0.62 / 24
	gx0 := margin + (inner-24*gs)/2
	gy0 := margin + (inner-24*gs)/2 + inner*0.01

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := float64(px) + (float64(sx)+0.5)/ss
					y := float64(py) + (float64(sy)+0.5)/ss
					if !roundedRect(x-margin, y-margin, inner, radius) {
						continue
					}
					t := (x + y - 2*margin) / (2 * inner)
					if t < 0 {
						t = 0
					} else if t > 1 {
						t = 1
					}
					cr, cg, cb := float64(lerp(top.R, bottom.R, t)), float64(lerp(top.G, bottom.G, t)), float64(lerp(top.B, bottom.B, t))
					if cov := glyph((x-gx0)/gs, (y-gy0)/gs); cov > 0 {
						cr, cg, cb = cr+(255-cr)*cov, cg+(255-cg)*cov, cb+(255-cb)*cov
					}
					r, g, b, a = r+cr, g+cg, b+cb, a+1
				}
			}
			if a == 0 {
				continue
			}
			n := float64(ss * ss)
			img.SetNRGBA(px, py, color.NRGBA{uint8(r/a + 0.5), uint8(g/a + 0.5), uint8(b/a + 0.5), uint8(a/n*255 + 0.5)})
		}
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// encodeICO packs PNG images into an .ico (PNG-compressed entries, Vista+).
func encodeICO(sizes []int, pngs [][]byte) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		e := make([]byte, 16)
		if s < 256 {
			e[0], e[1] = byte(s), byte(s)
		}
		binary.LittleEndian.PutUint16(e[4:6], 1)
		binary.LittleEndian.PutUint16(e[6:8], 32)
		binary.LittleEndian.PutUint32(e[8:12], uint32(len(pngs[i])))
		binary.LittleEndian.PutUint32(e[12:16], uint32(offset))
		buf.Write(e)
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
	println("wrote", path, len(data), "bytes")
}

func main() {
	os.MkdirAll("build/windows", 0o755)
	os.MkdirAll("frontend/public", 0o755)

	big := encodePNG(render(1024))
	write("build/appicon.png", big)
	write("frontend/public/appicon.png", big)

	// Rendering each size directly (rather than shrinking one image) keeps the
	// small icons crisp.
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	pngs := make([][]byte, len(sizes))
	for i, s := range sizes {
		pngs[i] = encodePNG(render(s))
	}
	write("build/icon.png", pngs[len(pngs)-1])
	write("build/windows/icon.ico", encodeICO(sizes, pngs))
	write("build/icon.ico", encodeICO([]int{256}, pngs[len(pngs)-1:]))
}
