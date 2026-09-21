//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
)

func main() {
	os.MkdirAll("build", 0755)
	size := 256
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	c := color.RGBA{124, 58, 237, 255}
	s := color.RGBA{80, 140, 80, 255}
	l := color.RGBA{90, 160, 90, 255}
	w := color.RGBA{255, 255, 255, 40}

	petals := [5][5]int{
		{128, 75, 20, 40, 0},
		{102, 95, 18, 36, 0},
		{154, 95, 18, 36, 0},
		{90, 115, 16, 30, 0},
		{166, 115, 16, 30, 0},
	}

	for _, p := range petals {
		cx, cy, rx, ry := p[0], p[1], p[2], p[3]
		for dy := -ry; dy <= ry; dy++ {
			for dx := -rx; dx <= rx; dx++ {
				if float64(dx*dx)/(float64(rx)*float64(rx))+float64(dy*dy)/(float64(ry)*float64(ry)) <= 1.0 {
					set(img, cx+dx, cy+dy, c)
				}
			}
		}
		for dy := -ry / 3; dy <= ry/3; dy++ {
			for dx := -rx / 2; dx <= rx/2; dx++ {
				if float64(dx*dx)/(float64(rx*rx/4))+float64(dy*dy)/(float64(ry*ry/9)) <= 1.0 {
					set(img, cx+dx, cy+dy, w)
				}
			}
		}
	}

	for y := 135; y < 210; y++ {
		for x := 124; x < 132; x++ {
			set(img, x, y, s)
		}
	}

	leaf(img, 128, 160, 1, l)
	leaf(img, 128, 180, -1, l)

	var buf bytes.Buffer
	png.Encode(&buf, img)
	os.WriteFile("build/icon.png", buf.Bytes(), 0644)

	ico := encodeICO(buf.Bytes())
	os.WriteFile("build/icon.ico", ico, 0644)
	println("done")
}

func set(img *image.RGBA, x, y int, c color.RGBA) {
	if x >= 0 && x < 256 && y >= 0 && y < 256 {
		img.Set(x, y, c)
	}
}

func leaf(img *image.RGBA, cx, cy, dir int, c color.RGBA) {
	for i := 0; i < 25; i++ {
		w := i
		if i > 12 {
			w = 25 - i
		}
		for dy := -w; dy <= w; dy++ {
			set(img, cx+i*dir, cy+dy, c)
		}
	}
}

func encodeICO(pngData []byte) []byte {
	buf := new(bytes.Buffer)
	binary.Write(buf, binary.LittleEndian, uint16(0))
	binary.Write(buf, binary.LittleEndian, uint16(1))
	binary.Write(buf, binary.LittleEndian, uint16(1))
	// ICONDIRENTRY: width, height (0 = 256), colors, reserved, planes, bit
	// depth, image size and the offset of the image data (6-byte ICONDIR +
	// this 16-byte entry).
	e := make([]byte, 16)
	binary.LittleEndian.PutUint16(e[4:6], 1)
	binary.LittleEndian.PutUint16(e[6:8], 32)
	binary.LittleEndian.PutUint32(e[8:12], uint32(len(pngData)))
	binary.LittleEndian.PutUint32(e[12:16], 22)
	binary.Write(buf, binary.LittleEndian, e)
	buf.Write(pngData)
	return buf.Bytes()
}
