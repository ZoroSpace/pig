// Command render-logo renders a simple rect-based SVG (pixel-art logos) to PNG.
//
// Supports: <svg viewBox>, <rect x y width height fill>, a single
// <g transform="translate(x,y)"> wrapper, and an optional full-canvas
// background rect. Output is center-fit within the target size, scaled
// by the largest integer factor that fits (keeps pixel-art edges sharp),
// padded with the SVG background color (or transparent when none).
//
// Usage: go run ./scripts/render-logo <in.svg> <out.png> <width> <height>
package main

import (
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
)

type svg struct {
	ViewBox string `xml:"viewBox,attr"`
	Rect    []rect `xml:"rect"`
	G       []g    `xml:"g"`
}

type g struct {
	Transform string `xml:"transform,attr"`
	Rect      []rect `xml:"rect"`
}

type rect struct {
	X      string `xml:"x,attr"`
	Y      string `xml:"y,attr"`
	W      string `xml:"width,attr"`
	H      string `xml:"height,attr"`
	Fill   string `xml:"fill,attr"`
}

func parseHex(s string) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	var r, g, b, a uint8 = 0, 0, 0, 255
	if len(s) == 3 {
		r = hexNibble(s[0]) * 17
		g = hexNibble(s[1]) * 17
		b = hexNibble(s[2]) * 17
	} else if len(s) >= 6 {
		r = hexByte(s[0:2])
		g = hexByte(s[2:4])
		b = hexByte(s[4:6])
		if len(s) == 8 {
			a = hexByte(s[6:8])
		}
	}
	return color.RGBA{r, g, b, a}
}

func hexNibble(c byte) uint8 {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

func hexByte(s string) uint8 { return hexNibble(s[0])<<4 | hexNibble(s[1]) }

func atof(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func parseTransform(t string) (int, int) {
	// "translate(20, 20)" -> (20, 20)
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "translate(")
	t = strings.TrimSuffix(t, ")")
	parts := strings.Split(t, ",")
	if len(parts) != 2 {
		return 0, 0
	}
	return atof(parts[0]), atof(parts[1])
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintf(os.Stderr, "usage: %s <in.svg> <out.png> <width> <height>\n", os.Args[0])
		os.Exit(2)
	}
	inPath, outPath := os.Args[1], os.Args[2]
	outW, _ := strconv.Atoi(os.Args[3])
	outH, _ := strconv.Atoi(os.Args[4])

	data, err := os.ReadFile(inPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	var s svg
	if err := xml.Unmarshal(data, &s); err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}

	parts := strings.Fields(s.ViewBox)
	viewW, _ := strconv.Atoi(parts[2])
	viewH, _ := strconv.Atoi(parts[3])

	// Collect pixel rects with final absolute coords + color.
	type px struct{ x, y, w, h int; c color.RGBA }
	var rects []px
	bg := color.RGBA{} // zero alpha => transparent when unset
	for _, r := range s.Rect {
		col := parseHex(r.Fill)
		if r.X == "" && r.Y == "" && r.W == string(viewW) && r.H == string(viewH) {
			bg = col
			continue
		}
		rects = append(rects, px{atof(r.X), atof(r.Y), atof(r.W), atof(r.H), col})
	}
	for _, g := range s.G {
		dx, dy := parseTransform(g.Transform)
		for _, r := range g.Rect {
			rects = append(rects, px{atof(r.X) + dx, atof(r.Y) + dy, atof(r.W), atof(r.H), parseHex(r.Fill)})
		}
	}

	// Largest integer scale that fits the target size, center-fit.
	scale := outW / viewW
	if h := outH / viewH; h < scale {
		scale = h
	}
	if scale < 1 {
		scale = 1
	}
	contentW, contentH := viewW*scale, viewH*scale
	offX, offY := (outW-contentW)/2, (outH-contentH)/2

	img := image.NewRGBA(image.Rect(0, 0, outW, outH))
	if bg.A > 0 {
		for y := 0; y < outH; y++ {
			for x := 0; x < outW; x++ {
				img.Set(x, y, bg)
			}
		}
	}
	for _, r := range rects {
		for y := offY + r.y*scale; y < offY+(r.y+r.h)*scale; y++ {
			for x := offX + r.x*scale; x < offX+(r.x+r.w)*scale; x++ {
				img.Set(x, y, r.c)
			}
		}
	}

	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
	fmt.Printf("rendered %s (%dx%d)\n", outPath, outW, outH)
}
