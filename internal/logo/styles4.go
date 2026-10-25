package logo

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// textMask renders a short string with any embedded font at a baseline position.
func textMask(file string, size float64, text string, x, y float64, centered bool) *image.Alpha {
	m := image.NewAlpha(image.Rect(0, 0, W, H))
	face, err := faceFor(file, size)
	if err != nil {
		return m
	}
	defer face.Close()
	if centered {
		x -= measure(face, text) / 2
	}
	d := &font.Drawer{Dst: m, Src: image.Opaque, Face: face, Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}}
	d.DrawString(text)
	return m
}

// ---- anime: brush lettering, rising sun and a sweeping brush stroke ----

func (r *renderer) anime() {
	tb := r.text.bounds
	cx, cy := center(tb)
	sunR := float32(math.Min(float64(tb.Dy())*0.95+30, 150))
	sx := cx + float32(tb.Dx())*0.18
	r.paintColor(blur(rasterMask(func(z *vector.Rasterizer) { circle(z, sx, cy, sunR+10) }), 12), withAlpha(hex("#ef4444"), 0.35), 0, 0)
	r.paint(rasterMask(func(z *vector.Rasterizer) { circle(z, sx, cy, sunR) }), gradient(image.Rect(0, int(cy-sunR), W, int(cy+sunR)), hex("#f87171"), hex("#dc2626"), hex("#991b1b")), 0, 0)
	// speed lines on both sides
	lines := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 14; i++ {
			y := float32(tb.Min.Y) + float32(r.rng.Intn(max(1, tb.Dy())))
			l := 40 + float32(r.rng.Intn(90))
			th := 1 + float32(r.rng.Intn(3))
			if i%2 == 0 {
				x := float32(tb.Min.X) - 10
				polygon(z, [][2]float32{{x - l, y}, {x, y - th}, {x, y + th}})
			} else {
				x := float32(tb.Max.X) + 10
				polygon(z, [][2]float32{{x + l, y}, {x, y - th}, {x, y + th}})
			}
		}
	})
	r.paintColor(lines, withAlpha(white, 0.75), 0, 0)
	// brush stroke under the text
	by := float32(tb.Max.Y) + 6
	stroke := rasterMask(func(z *vector.Rasterizer) {
		x0, x1 := float32(tb.Min.X)-10, float32(tb.Max.X)+20
		z.MoveTo(x0, by+2)
		z.QuadTo((x0+x1)/2, by-10, x1, by-4)
		z.QuadTo(x1-6, by+2, x1-30, by+6)
		z.QuadTo((x0+x1)/2, by+4, x0+4, by+12)
		z.ClosePath()
	})
	r.paintColor(stroke, hex("#111111"), 0, 0)
	ow := r.outlineWidth(0.06)
	r.paintColor(dilate(r.text.mask, ow), hex("#b91c1c"), 4, 4)
	r.paintColor(dilate(r.text.mask, ow), hex("#111111"), 0, 0)
	r.paintColor(r.text.mask, white, 0, 0)
}

// ---- silent: art-deco intertitle card with film grain and vignette ----

func (r *renderer) silent() {
	tb := r.text.bounds
	plate := image.Rect(tb.Min.X-50, tb.Min.Y-40, tb.Max.X+50, tb.Max.Y+40).Intersect(image.Rect(4, 4, W-4, H-4))
	pm := roundedRect(plate, 6)
	r.paintColor(blur(pm, 8), withAlpha(black, 0.6), 0, 6)
	r.paint(pm, gradient(plate, hex("#1c1c1c"), hex("#0b0b0b")), 0, 0)
	// double frame with corner diamonds
	outer := roundedRect(plate.Inset(10), 2)
	r.paintColor(subtract(outer, roundedRect(plate.Inset(12), 2)), hex("#e5e5e5"), 0, 0)
	r.paintColor(subtract(roundedRect(plate.Inset(17), 2), roundedRect(plate.Inset(18), 2)), withAlpha(hex("#e5e5e5"), 0.7), 0, 0)
	corners := rasterMask(func(z *vector.Rasterizer) {
		for _, c := range [][2]int{{plate.Min.X + 11, plate.Min.Y + 11}, {plate.Max.X - 11, plate.Min.Y + 11}, {plate.Min.X + 11, plate.Max.Y - 11}, {plate.Max.X - 11, plate.Max.Y - 11}} {
			x, y := float32(c[0]), float32(c[1])
			polygon(z, [][2]float32{{x, y - 9}, {x + 9, y}, {x, y + 9}, {x - 9, y}})
		}
	})
	r.paintColor(corners, hex("#e5e5e5"), 0, 0)
	r.paintColor(blur(r.text.mask, 2), withAlpha(white, 0.25), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#ffffff"), hex("#d4d4d4")), 0, 0)
	// film grain and vignette inside the plate
	cx, cy := center(plate)
	for y := plate.Min.Y; y < plate.Max.Y; y++ {
		for x := plate.Min.X; x < plate.Max.X; x++ {
			i := y*r.img.Stride + x*4
			if r.img.Pix[i+3] == 0 || pm.Pix[y*pm.Stride+x] < 250 {
				continue
			}
			dx := (float64(x) - float64(cx)) / float64(plate.Dx()/2)
			dy := (float64(y) - float64(cy)) / float64(plate.Dy()/2)
			v := 1 - 0.45*math.Min(1, (dx*dx+dy*dy)*0.6)
			n := (r.rng.Float64() - 0.5) * 34
			for k := 0; k < 3; k++ {
				r.img.Pix[i+k] = uint8(math.Max(0, math.Min(255, float64(r.img.Pix[i+k])*v+n)))
			}
		}
	}
	// scratches
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 2; i++ {
			x := float32(plate.Min.X + 20 + r.rng.Intn(max(1, plate.Dx()-40)))
			rect(z, x, float32(plate.Min.Y+4), x+0.8, float32(plate.Max.Y-4))
		}
	}), withAlpha(white, 0.25), 0, 0)
}

// ---- crayon: children's-book lettering with sun, cloud and grass ----

func (r *renderer) crayon() {
	tb := r.text.bounds
	// sun with rays
	sx, sy := float32(tb.Max.X)+8, float32(tb.Min.Y)-6
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		circle(z, sx, sy, 22)
		for i := 0; i < 10; i++ {
			a := float64(i) * math.Pi / 5
			ca, sa := float32(math.Cos(a)), float32(math.Sin(a))
			polygon(z, [][2]float32{{sx + ca*28 - sa*2.5, sy + sa*28 + ca*2.5}, {sx + ca*42, sy + sa*42}, {sx + ca*28 + sa*2.5, sy + sa*28 - ca*2.5}})
		}
	}), hex("#facc15"), 0, 0)
	// cloud
	cx, cy := float32(tb.Min.X)+10, float32(tb.Min.Y)-14
	cloud := rasterMask(func(z *vector.Rasterizer) {
		circle(z, cx, cy, 16)
		circle(z, cx+18, cy-8, 20)
		circle(z, cx+38, cy, 15)
		rect(z, cx, cy, cx+38, cy+15)
	})
	r.paintColor(dilate(cloud, 3), hex("#60a5fa"), 0, 0)
	r.paintColor(cloud, hex("#f0f9ff"), 0, 0)
	// grass
	gy := float32(tb.Max.Y) + 18
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		x0, x1 := float32(tb.Min.X)-20, float32(tb.Max.X)+20
		z.MoveTo(x0, gy+8)
		for x := x0; x <= x1; x += 9 {
			z.LineTo(x+4.5, gy-6-float32(r.rng.Intn(6)))
			z.LineTo(x+9, gy+2)
		}
		z.LineTo(x1, gy+10)
		z.ClosePath()
	}), hex("#22c55e"), 0, 0)
	// letters: one crayon colour each, wobbly darker outline, waxy texture
	palette := []color.RGBA{hex("#ef4444"), hex("#f97316"), hex("#eab308"), hex("#22c55e"), hex("#3b82f6"), hex("#a855f7"), hex("#ec4899")}
	fill := image.NewRGBA(image.Rect(0, 0, W, H))
	for i, g := range r.text.glyphs {
		c := palette[(i+r.rng.Intn(2))%len(palette)]
		for y := max(0, g.y0-20); y < min(H, g.y1+20); y++ {
			for x := max(0, g.x0-2); x < min(W, g.x1+2); x++ {
				fill.SetRGBA(x, y, c)
			}
		}
	}
	dark := image.NewRGBA(image.Rect(0, 0, W, H))
	for i := range fill.Pix {
		dark.Pix[i] = uint8(float64(fill.Pix[i]) * 0.55)
	}
	r.paint(dilate(r.text.mask, 3), dark, 1, 2)
	tex := cloneAlpha(r.text.mask)
	for i := range tex.Pix {
		if tex.Pix[i] > 0 && r.rng.Intn(100) < 22 {
			tex.Pix[i] = uint8(int(tex.Pix[i]) * (40 + r.rng.Intn(40)) / 100)
		}
	}
	r.paint(tex, fill, 0, 0)
}

// ---- mystery: foggy lettering, magnifying glass and question marks ----

func (r *renderer) mystery() {
	tb := r.text.bounds
	for i := 0; i < 5; i++ {
		x := float32(tb.Min.X - 40 + r.rng.Intn(tb.Dx()+80))
		q := textMask("IMFeENsc28P.ttf", 40+float64(r.rng.Intn(40)), "?", float64(x), float64(tb.Min.Y-10+r.rng.Intn(tb.Dy()+50)), true)
		r.paintColor(q, withAlpha(hex("#5eead4"), 0.18), 0, 0)
	}
	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(10, r.text.size*0.16))), 1.4), withAlpha(hex("#0f766e"), 0.6), 0, 0)
	r.paintColor(blur(r.text.mask, 3), withAlpha(black, 0.8), 0, 3)
	r.paint(r.text.mask, gradient(tb, hex("#f0fdfa"), hex("#99f6e4"), hex("#5eead4")), 0, 0)
	// fog drifting over the lower half of the letters
	fog := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 7; i++ {
			x := float32(tb.Min.X - 30 + r.rng.Intn(tb.Dx()+60))
			y := float32(tb.Max.Y) - float32(tb.Dy())*0.25 + float32(r.rng.Intn(30))
			circle(z, x, y, 26+float32(r.rng.Intn(24)))
		}
	})
	r.paintColor(blur(fog, 16), withAlpha(hex("#e2e8f0"), 0.35), 0, 0)
	// magnifying glass on the last letter
	last := r.text.glyphs[len(r.text.glyphs)-1]
	gx, gy := float32(last.x1)+4, float32(last.y0+last.y1)/2-6
	gr := float32(math.Max(22, float64(last.y1-last.y0)*0.32))
	lens := rasterMask(func(z *vector.Rasterizer) { circle(z, gx, gy, gr) })
	r.paintColor(lens, withAlpha(hex("#ccfbf1"), 0.18), 0, 0)
	ring := subtract(rasterMask(func(z *vector.Rasterizer) { circle(z, gx, gy, gr+5) }), lens)
	handle := rasterMask(func(z *vector.Rasterizer) {
		a := float32(math.Pi / 4)
		ca, sa := float32(math.Cos(float64(a))), float32(math.Sin(float64(a)))
		x0, y0 := gx+ca*(gr+3), gy+sa*(gr+3)
		x1, y1 := gx+ca*(gr+gr*1.1), gy+sa*(gr+gr*1.1)
		w := float32(5)
		polygon(z, [][2]float32{{x0 - sa*w, y0 + ca*w}, {x1 - sa*w, y1 + ca*w}, {x1 + sa*w, y1 - ca*w}, {x0 + sa*w, y0 - ca*w}})
	})
	r.paintColor(blur(union2(ring, handle), 3), withAlpha(black, 0.6), 0, 3)
	r.paintColor(ring, hex("#d4d4d8"), 0, 0)
	r.paintColor(handle, hex("#78350f"), 0, 0)
	r.paintColor(rasterMask(func(z *vector.Rasterizer) { circle(z, gx-gr*0.4, gy-gr*0.4, gr*0.18) }), withAlpha(white, 0.6), 0, 0)
}

func union2(a, b *image.Alpha) *image.Alpha {
	out := cloneAlpha(a)
	union(out, b)
	return out
}

// ---- bollywood: magenta-to-saffron lettering on a golden mandala ----

func (r *renderer) bollywood() {
	tb := r.text.bounds
	cx, cy := center(tb)
	mandala := rasterMask(func(z *vector.Rasterizer) {
		for ring, n := range []int{12, 18, 24} {
			rad := float32(60 + ring*42)
			pr := float32(14 + ring*4)
			for i := 0; i < n; i++ {
				a := float64(i) * 2 * math.Pi / float64(n)
				x, y := cx+rad*float32(math.Cos(a)), cy+rad*0.62*float32(math.Sin(a))
				circle(z, x, y, pr)
			}
		}
	})
	r.paintColor(subtract(dilate(mandala, 2), mandala), withAlpha(hex("#fbbf24"), 0.5), 0, 0)
	ow := r.outlineWidth(0.05)
	r.paintColor(blur(dilate(r.text.mask, ow*2), 6), withAlpha(hex("#3b0764"), 0.8), 0, 4)
	r.paintColor(dilate(r.text.mask, ow*2), hex("#4a044e"), 0, 0)
	r.paint(dilate(r.text.mask, ow), gradient(tb, hex("#fde68a"), hex("#d97706"), hex("#fcd34d")), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#f472b6"), hex("#db2777"), hex("#f97316")), 0, 0)
	dots := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 16; i++ {
			a := float64(i) * 2 * math.Pi / 16
			circle(z, cx+float32(tb.Dx())*0.62*float32(math.Cos(a)), cy+float32(tb.Dy())*0.95*float32(math.Sin(a)), 3)
		}
	})
	r.paintColor(dots, hex("#fde68a"), 0, 0)
}

// ---- cooking: retro diner sign with fork and knife ----

func (r *renderer) cooking() {
	tb := r.text.bounds
	sign := image.Rect(tb.Min.X-40, tb.Min.Y-30, tb.Max.X+40, tb.Max.Y+34).Intersect(image.Rect(6, 6, W-6, H-6))
	sm := roundedRect(sign, float32(sign.Dy())/2.4)
	r.paintColor(blur(sm, 9), withAlpha(black, 0.55), 0, 7)
	r.paint(sm, gradient(sign, hex("#f1f5f9"), hex("#94a3b8"), hex("#e2e8f0")), 0, 0)
	inner := roundedRect(sign.Inset(8), float32(sign.Dy())/2.4-6)
	r.paint(inner, gradient(sign, hex("#5eead4"), hex("#14b8a6"), hex("#0f766e")), 0, 0)
	// checkered strip along the bottom of the sign
	check := rasterMask(func(z *vector.Rasterizer) {
		y0 := float32(sign.Max.Y) - 26
		for x := sign.Min.X; x < sign.Max.X; x += 10 {
			off := float32(0)
			if (x/10)%2 == 1 {
				off = 5
			}
			rect(z, float32(x), y0+off, float32(x)+5, y0+off+5)
			rect(z, float32(x)+5, y0+5-off, float32(x)+10, y0+10-off)
		}
	})
	r.paintColor(intersect(check, inner), withAlpha(white, 0.5), 0, 0)
	ow := r.outlineWidth(0.06)
	r.paintColor(dilate(r.text.mask, ow), white, 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#f87171"), hex("#dc2626"), hex("#991b1b")), 0, 0)
	// fork and knife on both sides of the sign
	cy := float32(sign.Min.Y+sign.Max.Y) / 2
	k := float32(1.6)
	cutlery := rasterMask(func(z *vector.Rasterizer) {
		if fx := float32(sign.Min.X) - 22; fx > 14 {
			rect(z, fx-2*k, cy-4*k, fx+2*k, cy+26*k)
			for i := 0; i < 4; i++ {
				rect(z, fx-6*k+float32(i)*3.4*k, cy-26*k, fx-4.6*k+float32(i)*3.4*k, cy-8*k)
			}
			rect(z, fx-6*k, cy-10*k, fx+6*k, cy-4*k)
		}
		if kx := float32(sign.Max.X) + 22; kx < W-14 {
			polygon(z, [][2]float32{{kx - 2*k, cy - 26*k}, {kx + 4*k, cy - 20*k}, {kx + 3*k, cy}, {kx - 2*k, cy}})
			rect(z, kx-2.2*k, cy, kx+2.2*k, cy+26*k)
		}
	})
	r.paintColor(blur(cutlery, 3), withAlpha(black, 0.5), 0, 3)
	r.paint(cutlery, gradient(image.Rect(0, int(cy-30*k), W, int(cy+30*k)), hex("#f8fafc"), hex("#94a3b8")), 0, 0)
}

// ---- atomic: 50s space-age lettering with planet and boomerang stars ----

func (r *renderer) atomic() {
	tb := r.text.bounds
	px, py := float32(tb.Max.X)-10, float32(tb.Min.Y)-4
	pr := float32(26)
	planet := rasterMask(func(z *vector.Rasterizer) { circle(z, px, py, pr) })
	r.paint(planet, gradient(image.Rect(0, int(py-pr), W, int(py+pr)), hex("#fdba74"), hex("#ea580c")), 0, 0)
	ring := rasterMask(func(z *vector.Rasterizer) {
		const k = 0.5523
		rx, ry := pr*1.9, pr*0.45
		ell := func(rx, ry float32) {
			z.MoveTo(px+rx, py)
			z.CubeTo(px+rx, py+ry*k, px+rx*k, py+ry, px, py+ry)
			z.CubeTo(px-rx*k, py+ry, px-rx, py+ry*k, px-rx, py)
			z.CubeTo(px-rx, py-ry*k, px-rx*k, py-ry, px, py-ry)
			z.CubeTo(px+rx*k, py-ry, px+rx, py-ry*k, px+rx, py)
			z.ClosePath()
		}
		ell(rx, ry)
	})
	inner := rasterMask(func(z *vector.Rasterizer) {
		const k = 0.5523
		rx, ry := pr*1.55, pr*0.28
		z.MoveTo(px+rx, py)
		z.CubeTo(px+rx, py+ry*k, px+rx*k, py+ry, px, py+ry)
		z.CubeTo(px-rx*k, py+ry, px-rx, py+ry*k, px-rx, py)
		z.CubeTo(px-rx, py-ry*k, px-rx*k, py-ry, px, py-ry)
		z.CubeTo(px+rx*k, py-ry, px+rx, py-ry*k, px+rx, py)
		z.ClosePath()
	})
	r.paintColor(subtract(ring, inner), hex("#5eead4"), 0, 0)
	// boomerang stars
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 5; i++ {
			x := float32(tb.Min.X - 30 + r.rng.Intn(tb.Dx()+60))
			y := float32(tb.Min.Y - 30 + r.rng.Intn(tb.Dy()+70))
			if image.Pt(int(x), int(y)).In(tb.Inset(-4)) {
				continue
			}
			star(z, x, y, 9+float32(r.rng.Intn(9)))
		}
	}), hex("#fde68a"), 0, 0)
	ow := r.outlineWidth(0.05)
	r.paintColor(dilate(r.text.mask, ow), hex("#ea580c"), 5, 5)
	r.paintColor(dilate(r.text.mask, ow), hex("#134e4a"), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#fef3c7"), hex("#99f6e4"), hex("#2dd4bf")), 0, 0)
}

// ---- graffiti: spray-paint lettering with splatters and drips ----

func (r *renderer) graffiti() {
	tb := r.text.bounds
	for i, c := range []string{"#ec4899", "#22d3ee", "#facc15"} {
		x := float32(tb.Min.X) + float32(tb.Dx())*(0.15+0.35*float32(i)) + float32(r.rng.Intn(20))
		y := float32(tb.Min.Y+tb.Max.Y)/2 + float32(r.rng.Intn(30)-15)
		sp := rasterMask(func(z *vector.Rasterizer) {
			circle(z, x, y, 34+float32(r.rng.Intn(16)))
			for k := 0; k < 14; k++ {
				a := r.rng.Float64() * 2 * math.Pi
				d := 40 + float32(r.rng.Intn(40))
				circle(z, x+d*float32(math.Cos(a)), y+d*0.7*float32(math.Sin(a)), 2+float32(r.rng.Intn(5)))
			}
		})
		r.paintColor(sp, withAlpha(hex(c), 0.55), 0, 0)
	}
	mask := cloneAlpha(r.text.mask)
	for i := 0; i < 3+len(r.text.glyphs)/3; i++ {
		g := r.text.glyphs[r.rng.Intn(len(r.text.glyphs))]
		if g.x1-g.x0 < 8 {
			continue
		}
		x := g.x0 + 4 + r.rng.Intn(max(1, g.x1-g.x0-6))
		bottom := -1
		for y := min(H-1, g.y1); y >= g.y0; y-- {
			if y >= 0 && mask.Pix[y*mask.Stride+x] > 160 {
				bottom = y
				break
			}
		}
		if bottom < 0 {
			continue
		}
		w := float32(2 + r.rng.Intn(3))
		l := float32(10 + r.rng.Intn(30))
		fx, fy := float32(x), float32(bottom-1)
		union(mask, rasterMask(func(z *vector.Rasterizer) {
			rect(z, fx-w/2, fy, fx+w/2, fy+l)
			circle(z, fx, fy+l, w*0.9)
		}))
	}
	ow := r.outlineWidth(0.06)
	r.paintColor(dilate(mask, ow), hex("#0a0a0a"), 3, 4)
	r.paintColor(dilate(mask, ow), hex("#0a0a0a"), 0, 0)
	r.paint(mask, gradient(tb, hex("#bef264"), hex("#22d3ee"), hex("#a855f7")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.5), white, -2, -2)
}

// ---- manuscript: parchment with an illuminated initial and a wax seal ----

func (r *renderer) manuscript() {
	tb := r.text.bounds
	box := image.Rect(tb.Min.X-50, tb.Min.Y-36, tb.Max.X+50, tb.Max.Y+40).Intersect(image.Rect(6, 6, W-6, H-6))
	parch := rasterMask(func(z *vector.Rasterizer) {
		var pts [][2]float32
		jit := func() float32 { return float32(r.rng.Intn(7) - 3) }
		for x := box.Min.X; x <= box.Max.X; x += 14 {
			pts = append(pts, [2]float32{float32(x), float32(box.Min.Y) + jit()})
		}
		for y := box.Min.Y; y <= box.Max.Y; y += 14 {
			pts = append(pts, [2]float32{float32(box.Max.X) + jit(), float32(y)})
		}
		for x := box.Max.X; x >= box.Min.X; x -= 14 {
			pts = append(pts, [2]float32{float32(x), float32(box.Max.Y) + jit()})
		}
		for y := box.Max.Y; y >= box.Min.Y; y -= 14 {
			pts = append(pts, [2]float32{float32(box.Min.X) + jit(), float32(y)})
		}
		polygon(z, pts)
	})
	r.paintColor(blur(parch, 8), withAlpha(black, 0.5), 0, 6)
	r.paint(parch, gradient(box, hex("#f5e6c4"), hex("#ead3a2"), hex("#dcbf84")), 0, 0)
	// burnt edges
	edge := subtract(parch, blur(scaleAlpha(subtract(parch, dilate(subtract(rasterMask(func(z *vector.Rasterizer) { rect(z, 0, 0, W, H) }), parch), 10)), 1), 6))
	r.paintColor(edge, withAlpha(hex("#8b5a2b"), 0.5), 0, 0)
	// illuminated initial
	g := r.text.glyphs[0]
	ib := image.Rect(g.x0-6, g.y0+int(float64(g.y1-g.y0)*0.08), g.x1+6, g.y1-int(float64(g.y1-g.y0)*0.12))
	init := roundedRect(ib, 4)
	r.paint(init, gradient(ib, hex("#b91c1c"), hex("#7f1d1d")), 0, 0)
	r.paintColor(subtract(dilate(init, 3), init), hex("#d4a017"), 0, 0)
	firstMask := intersect(r.text.mask, rasterMask(func(z *vector.Rasterizer) { rect(z, float32(g.x0-2), 0, float32(g.x1+1), H) }))
	rest := subtract(r.text.mask, firstMask)
	r.paintColor(rest, hex("#3b2414"), 0, 0)
	r.paint(firstMask, gradient(ib, hex("#fde68a"), hex("#d4a017")), 0, 0)
	// wax seal
	sx, sy := float32(box.Max.X)-26, float32(box.Max.Y)-18
	seal := rasterMask(func(z *vector.Rasterizer) {
		var pts [][2]float32
		for i := 0; i < 18; i++ {
			a := float64(i) * 2 * math.Pi / 18
			rad := float32(20 + r.rng.Intn(4))
			pts = append(pts, [2]float32{sx + rad*float32(math.Cos(a)), sy + rad*float32(math.Sin(a))})
		}
		polygon(z, pts)
	})
	r.paintColor(blur(seal, 3), withAlpha(black, 0.5), 0, 3)
	r.paint(seal, gradient(image.Rect(0, int(sy-22), W, int(sy+22)), hex("#dc2626"), hex("#7f1d1d")), 0, 0)
	r.paintColor(subtract(rasterMask(func(z *vector.Rasterizer) { circle(z, sx, sy, 13) }), rasterMask(func(z *vector.Rasterizer) { circle(z, sx, sy, 11) })), withAlpha(hex("#fca5a5"), 0.6), 0, 0)
}

// ---- vhs: hand-written cassette label with rainbow stripes and a rental sticker ----

func (r *renderer) vhs() {
	tb := r.text.bounds
	label := image.Rect(tb.Min.X-44, tb.Min.Y-30, tb.Max.X+44, tb.Max.Y+52).Intersect(image.Rect(8, 8, W-8, H-8))
	lm := roundedRect(label, 8)
	r.paintColor(blur(lm, 8), withAlpha(black, 0.55), 0, 7)
	r.paint(lm, gradient(label, hex("#fafaf9"), hex("#e7e5e4")), 0, 0)
	// rainbow stripes along the bottom
	stripes := []string{"#ef4444", "#f97316", "#facc15", "#22c55e", "#3b82f6"}
	sh := float32(5)
	y0 := float32(label.Max.Y) - 12 - sh*float32(len(stripes))
	for i, c := range stripes {
		y := y0 + float32(i)*sh
		r.paintColor(intersect(rasterMask(func(z *vector.Rasterizer) { rect(z, float32(label.Min.X), y, float32(label.Max.X), y+sh) }), lm), hex(c), 0, 0)
	}
	// ruled writing lines
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for step, y := max(1, tb.Dy()/2), tb.Min.Y+tb.Dy()/2; y <= tb.Max.Y+4; y += step {
			rect(z, float32(label.Min.X)+16, float32(y), float32(label.Max.X)-16, float32(y)+1)
		}
	}), withAlpha(hex("#93c5fd"), 0.6), 0, 0)
	r.paintColor(r.text.mask, hex("#1f2937"), 0, 0)
	// rental price sticker
	px, py := float32(label.Max.X)-24, float32(label.Min.Y)+6
	sticker := rasterMask(func(z *vector.Rasterizer) { circle(z, px, py, 26) })
	r.paintColor(blur(sticker, 3), withAlpha(black, 0.4), 0, 2)
	r.paintColor(sticker, hex("#f97316"), 0, 0)
	r.paintColor(textMask("BebasNeue-Regular.ttf", 18, "LEIH", float64(px), float64(py)-2, true), white, 0, 0)
	r.paintColor(textMask("BebasNeue-Regular.ttf", 17, "2,99", float64(px), float64(py)+14, true), white, 0, 0)
	// tracking noise lines
	noise := image.NewAlpha(image.Rect(0, 0, W, H))
	for i := 0; i < 3; i++ {
		y := label.Min.Y + r.rng.Intn(max(1, label.Dy()))
		for x := 0; x < W; x++ {
			if r.rng.Intn(3) > 0 {
				noise.Pix[y*noise.Stride+x] = uint8(60 + r.rng.Intn(80))
			}
		}
	}
	r.paintColor(intersect(noise, lm), white, 0, 0)
}
