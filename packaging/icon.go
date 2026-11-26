//go:build ignore

// Draws the Kendra icon: a faceted kaleidoscope gem (six coloured triangles, each cut
// into a light and a dark half) on a deep moss tile. Writes a 1024px PNG and the SVG.
//
//	go run packaging/icon.go out.png [out.svg]
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
)

type pt struct{ x, y float64 }

var palette = []string{"#34d6a6", "#f2b24a", "#ff7b66", "#5ab8ff", "#b69cff", "#e7f6a3"}

const (
	size   = 1024
	inset  = 100.0
	radius = 185.0
	gemR   = 330.0
	shrink = 0.965 // facets are shrunk a little so a thin dark seam shows between them
)

func hex(s string) [3]float64 {
	var r, g, b int
	fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b)
	return [3]float64{float64(r), float64(g), float64(b)}
}

func vertices(r float64) [6]pt {
	var v [6]pt
	for k := range v {
		a := (-90 + 60*float64(k)) * math.Pi / 180
		v[k] = pt{math.Cos(a) * r, math.Sin(a) * r}
	}
	return v
}

func inTri(p, a, b, c pt) bool {
	s := func(p1, p2, p3 pt) float64 { return (p1.x-p3.x)*(p2.y-p3.y) - (p2.x-p3.x)*(p1.y-p3.y) }
	d1, d2, d3 := s(p, a, b), s(p, b, c), s(p, c, a)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

func scale(p, about pt, f float64) pt {
	return pt{about.x + (p.x-about.x)*f, about.y + (p.y-about.y)*f}
}

func inTile(x, y float64) bool {
	lo, hi := inset, size-inset
	if x < lo || x > hi || y < lo || y > hi {
		return false
	}
	cx := math.Min(math.Max(x, lo+radius), hi-radius)
	cy := math.Min(math.Max(y, lo+radius), hi-radius)
	return math.Hypot(x-cx, y-cy) <= radius
}

func mix(a [3]float64, f float64) [3]float64 { return [3]float64{a[0] * f, a[1] * f, a[2] * f} }

// sample returns the colour under one point, or ok=false outside the tile.
func sample(x, y float64) (c [3]float64, ok bool) {
	if !inTile(x, y) {
		return c, false
	}
	t := (y - inset) / (size - 2*inset)
	top, bot := hex("#14412f"), hex("#06100c")
	c = [3]float64{top[0] + (bot[0]-top[0])*t, top[1] + (bot[1]-top[1])*t, top[2] + (bot[2]-top[2])*t}
	p := pt{x - size/2, y - size/2}
	v := vertices(gemR)
	for k := 0; k < 6; k++ {
		a, b := v[k], v[(k+1)%6]
		o := pt{}
		cen := pt{(a.x + b.x) / 3, (a.y + b.y) / 3}
		sa, sb, so := scale(a, cen, shrink), scale(b, cen, shrink), scale(o, cen, shrink)
		if !inTri(p, so, sa, sb) {
			continue
		}
		mid := pt{(sa.x + sb.x) / 2, (sa.y + sb.y) / 2}
		base := hex(palette[k])
		if inTri(p, so, sa, mid) {
			return base, true
		}
		return mix(base, 0.74), true
	}
	if math.Hypot(p.x, p.y) < 44 {
		return hex("#06100c"), true
	}
	return c, true
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run packaging/icon.go out.png [out.svg]")
		os.Exit(2)
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	const ss = 3
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, n float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					if c, ok := sample(float64(x)+(float64(sx)+.5)/ss, float64(y)+(float64(sy)+.5)/ss); ok {
						r, g, b, n = r+c[0], g+c[1], b+c[2], n+1
					}
				}
			}
			if n > 0 {
				a := n / (ss * ss)
				img.SetRGBA(x, y, color.RGBA{uint8(r / n * a), uint8(g / n * a), uint8(b / n * a), uint8(a * 255)})
			}
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
	f.Close()
	if len(os.Args) > 2 {
		if err := os.WriteFile(os.Args[2], []byte(svg()), 0o644); err != nil {
			panic(err)
		}
	}
}

func svg() string {
	const k = 64.0 / size // 1024 canvas -> 64 viewBox
	v := vertices(gemR)
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><title>Kendra</title>` + "\n")
	b.WriteString(`<defs><linearGradient id="t" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#14412f"/><stop offset="1" stop-color="#06100c"/></linearGradient></defs>` + "\n")
	b.WriteString(fmt.Sprintf(`<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" fill="url(#t)"/>`+"\n", inset*k, inset*k, (size-2*inset)*k, (size-2*inset)*k, radius*k))
	b.WriteString(`<g transform="translate(32 32)">` + "\n")
	for i := 0; i < 6; i++ {
		a, c := v[i], v[(i+1)%6]
		cen := pt{(a.x + c.x) / 3, (a.y + c.y) / 3}
		sa, sc, so := scale(a, cen, shrink), scale(c, cen, shrink), scale(pt{}, cen, shrink)
		mid := pt{(sa.x + sc.x) / 2, (sa.y + sc.y) / 2}
		dark := hex(palette[i])
		d := mix(dark, 0.74)
		b.WriteString(fmt.Sprintf(`<polygon points="%.2f,%.2f %.2f,%.2f %.2f,%.2f" fill="%s"/>`+"\n", so.x*k, so.y*k, sa.x*k, sa.y*k, mid.x*k, mid.y*k, palette[i]))
		b.WriteString(fmt.Sprintf(`<polygon points="%.2f,%.2f %.2f,%.2f %.2f,%.2f" fill="#%02x%02x%02x"/>`+"\n", so.x*k, so.y*k, mid.x*k, mid.y*k, sc.x*k, sc.y*k, int(d[0]), int(d[1]), int(d[2])))
	}
	b.WriteString(`<circle r="2.75" fill="#06100c"/></g></svg>` + "\n")
	return b.String()
}
