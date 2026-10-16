package logo

import (
	"image"
	"math"

	"golang.org/x/image/vector"
)

// ---- shape helpers ----

func polygon(z *vector.Rasterizer, pts [][2]float32) {
	z.MoveTo(pts[0][0], pts[0][1])
	for _, p := range pts[1:] {
		z.LineTo(p[0], p[1])
	}
	z.ClosePath()
}

// star5 draws a five-pointed star.
func star5(z *vector.Rasterizer, cx, cy, r float32) {
	var pts [][2]float32
	for i := 0; i < 10; i++ {
		rad := r
		if i%2 == 1 {
			rad = r * 0.42
		}
		a := float64(i)*math.Pi/5 - math.Pi/2
		pts = append(pts, [2]float32{cx + rad*float32(math.Cos(a)), cy + rad*float32(math.Sin(a))})
	}
	polygon(z, pts)
}

// bat draws a small bat silhouette (symmetric wings with scalloped edges).
func bat(z *vector.Rasterizer, cx, cy, s float32) {
	half := [][2]float32{
		{0, -0.16}, {0.07, -0.4}, {0.12, -0.18}, {0.26, -0.3}, {0.55, -0.5}, {1.0, -0.42},
		{0.86, -0.18}, {0.78, 0.08}, {0.62, -0.06}, {0.5, 0.14}, {0.36, 0.0}, {0.22, 0.2}, {0.1, 0.08}, {0, 0.3},
	}
	var pts [][2]float32
	for _, p := range half {
		pts = append(pts, [2]float32{cx + p[0]*s, cy + p[1]*s})
	}
	for i := len(half) - 2; i >= 1; i-- {
		pts = append(pts, [2]float32{cx - half[i][0]*s, cy + half[i][1]*s})
	}
	polygon(z, pts)
}

// intersect keeps only the parts of a that are covered by b.
func intersect(a, b *image.Alpha) *image.Alpha {
	out := cloneAlpha(a)
	for i := range out.Pix {
		out.Pix[i] = uint8(int(out.Pix[i]) * int(b.Pix[i]) / 255)
	}
	return out
}

func subtract(a, b *image.Alpha) *image.Alpha {
	out := cloneAlpha(a)
	for i := range out.Pix {
		out.Pix[i] = uint8(clamp(int(out.Pix[i])-int(b.Pix[i]), 0, 255))
	}
	return out
}

func center(r image.Rectangle) (float32, float32) {
	return float32(r.Min.X+r.Max.X) / 2, float32(r.Min.Y+r.Max.Y) / 2
}

// ---- magic: whimsical gold lettering, arcane aura and a sparkling wand trail ----

func (r *renderer) magic() {
	tb := r.text.bounds
	aura := blur(r.text.mask, int(math.Max(12, r.text.size*0.2)))
	r.paintColor(scaleAlpha(aura, 1.5), withAlpha(hex("#3b1d8f"), 0.95), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 5), 1.2), withAlpha(hex("#7dd3fc"), 0.55), 0, 0)

	// A wand trail: a swooping arc under the text made of fading sparkle dots.
	x0, x1 := float32(tb.Min.X)-10, float32(tb.Max.X)+20
	baseY := float32(tb.Max.Y) + 12
	trail := rasterMask(func(z *vector.Rasterizer) {
		n := 46
		for i := 0; i < n; i++ {
			t := float32(i) / float32(n-1)
			x := x0 + (x1-x0)*t
			y := baseY + float32(math.Sin(float64(t)*math.Pi))*14 - t*t*18 + float32(r.rng.NormFloat64()*2)
			rad := 0.6 + 2.6*t
			circle(z, x, y, rad)
		}
	})
	r.paintColor(blur(trail, 3), withAlpha(hex("#fde68a"), 0.9), 0, 0)
	r.paintColor(trail, hex("#fffbeb"), 0, 0)
	tipX, tipY := x1, baseY-18
	r.paintColor(blur(rasterMask(func(z *vector.Rasterizer) { star(z, tipX, tipY, 22) }), 4), withAlpha(hex("#fde68a"), 0.9), 0, 0)
	r.paintColor(rasterMask(func(z *vector.Rasterizer) { star(z, tipX, tipY, 14) }), white, 0, 0)

	r.paint(r.text.mask, gradient(tb, hex("#fffbea"), hex("#f6d77a"), hex("#d4a017"), hex("#fff1b8")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.25), white, 0, -1)
	// floating motes
	motes := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 14; i++ {
			circle(z, float32(tb.Min.X-30+r.rng.Intn(tb.Dx()+60)), float32(tb.Min.Y-40+r.rng.Intn(tb.Dy()/2+40)), 1+float32(r.rng.Float64()*2))
		}
	})
	r.paintColor(motes, withAlpha(hex("#e0e7ff"), 0.8), 0, 0)
}

// ---- epic: engraved gold capitals glowing like a freshly forged ring ----

func (r *renderer) epic() {
	tb := r.text.bounds
	// rising embers
	embers := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 34; i++ {
			x := float32(tb.Min.X - 20 + r.rng.Intn(tb.Dx()+40))
			y := float32(tb.Min.Y - 50 + r.rng.Intn(tb.Dy()+90))
			circle(z, x, y, 0.8+float32(r.rng.Float64()*2.4))
		}
	})
	r.paintColor(blur(embers, 2), withAlpha(hex("#ff7a18"), 0.9), 0, 0)
	r.paintColor(embers, hex("#ffd28a"), 0, 0)

	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(10, r.text.size*0.18))), 1.4), withAlpha(hex("#ff5a00"), 0.75), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 4), 1.2), withAlpha(hex("#ffb347"), 0.7), 0, 0)
	ow := r.outlineWidth(0.035)
	r.paintColor(dilate(r.text.mask, ow), hex("#2a1405"), 0, 0)
	// bevel: dark lower edge, bright upper edge, metallic fill
	r.paintColor(r.text.mask, hex("#5c3a0a"), 0, 2)
	r.paintColor(r.text.mask, hex("#fff4c2"), 0, -1)
	r.paint(r.text.mask, gradient(tb, hex("#fff1b0"), hex("#e2a93b"), hex("#9a6418"), hex("#f3c969"), hex("#b8801f")), 0, 0)
}

// ---- fairytale: storybook lettering on a folded parchment ribbon ----

func (r *renderer) fairytale() {
	tb := r.text.bounds
	padX, padY := float32(34), float32(18)
	x0, x1 := float32(tb.Min.X)-padX, float32(tb.Max.X)+padX
	y0, y1 := float32(tb.Min.Y)-padY, float32(tb.Max.Y)+padY
	bend := float32(10)
	tail := float32(math.Min(56, float64(x0-6)))
	// back tails (darker), drawn behind the main band
	tails := rasterMask(func(z *vector.Rasterizer) {
		h := (y1 - y0) * 0.8
		ty := y0 + (y1-y0)*0.35
		polygon(z, [][2]float32{{x0 + 14, ty}, {x0 - tail, ty}, {x0 - tail + 18, ty + h/2}, {x0 - tail, ty + h}, {x0 + 14, ty + h}})
		polygon(z, [][2]float32{{x1 - 14, ty}, {x1 + tail, ty}, {x1 + tail - 18, ty + h/2}, {x1 + tail, ty + h}, {x1 - 14, ty + h}})
	})
	r.paintColor(blur(tails, 6), withAlpha(black, 0.4), 0, 6)
	r.paint(tails, gradient(tb, hex("#b88a4a"), hex("#8a5f2c")), 0, 0)
	band := rasterMask(func(z *vector.Rasterizer) {
		z.MoveTo(x0, y0+bend)
		z.QuadTo((x0+x1)/2, y0-bend, x1, y0+bend)
		z.LineTo(x1, y1+bend)
		z.QuadTo((x0+x1)/2, y1-bend, x0, y1+bend)
		z.ClosePath()
	})
	r.paintColor(blur(band, 8), withAlpha(black, 0.45), 0, 7)
	r.paint(band, gradient(image.Rect(0, int(y0), W, int(y1)), hex("#fbf0d0"), hex("#efd9a7"), hex("#e2c487")), 0, 0)
	// thin inner border
	inner := rasterMask(func(z *vector.Rasterizer) {
		z.MoveTo(x0+7, y0+bend+6)
		z.QuadTo((x0+x1)/2, y0-bend+6, x1-7, y0+bend+6)
		z.LineTo(x1-7, y1+bend-6)
		z.QuadTo((x0+x1)/2, y1-bend-6, x0+7, y1+bend-6)
		z.ClosePath()
	})
	r.paintColor(subtract(dilate(inner, 2), inner), withAlpha(hex("#a0702f"), 0.7), 0, 0)
	r.paintColor(blur(r.text.mask, 2), withAlpha(hex("#ffffff"), 0.6), 0, 1)
	r.paint(r.text.mask, gradient(tb, hex("#1f6f5c"), hex("#0f4c3a")), 0, 0)
	// tiny golden stars around
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 5; i++ {
			star5(z, float32(r.rng.Intn(W)), float32(8+r.rng.Intn(int(y0)-4+1)), 5+float32(r.rng.Intn(5)))
			star5(z, float32(r.rng.Intn(W)), float32(int(y1)+18+r.rng.Intn(max(1, H-int(y1)-24))), 5+float32(r.rng.Intn(5)))
		}
	}), hex("#f6d77a"), 0, 0)
}

// ---- space: chrome lettering with a horizon split, starfield and lens flare ----

func (r *renderer) space() {
	tb := r.text.bounds
	stars := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 70; i++ {
			circle(z, float32(r.rng.Intn(W)), float32(r.rng.Intn(H)), 0.5+float32(r.rng.Float64()*1.4))
		}
	})
	r.paintColor(stars, withAlpha(white, 0.75), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(10, r.text.size*0.15))), 1.2), withAlpha(hex("#2563eb"), 0.7), 0, 0)
	ow := r.outlineWidth(0.04)
	r.paintColor(dilate(r.text.mask, ow), hex("#0b1026"), 0, 0)
	// chrome: sky above the horizon, ground below, with a hard split in the middle
	chrome := image.NewRGBA(image.Rect(0, 0, W, H))
	bands := lineBands(r.text.mask)
	for _, b := range bands {
		mid := b[0] + (b[1]-b[0])*52/100
		top := gradient(image.Rect(0, b[0], W, mid), hex("#e0f2fe"), hex("#7dd3fc"), hex("#f8fafc"))
		bottom := gradient(image.Rect(0, mid, W, b[1]), hex("#334155"), hex("#94a3b8"), hex("#e2e8f0"))
		for y := max(0, b[0]-2); y < min(H, b[1]+2); y++ {
			src := top
			if y >= mid {
				src = bottom
			}
			copy(chrome.Pix[y*chrome.Stride:(y+1)*chrome.Stride], src.Pix[y*src.Stride:(y+1)*src.Stride])
		}
	}
	r.paint(r.text.mask, chrome, 0, 0)
	mid := tb.Min.Y + tb.Dy()/2
	if len(bands) > 0 {
		mid = bands[0][0] + (bands[0][1]-bands[0][0])*52/100
	}
	// lens flare across the horizon line
	cx, _ := center(tb)
	fy := float32(mid)
	flare := rasterMask(func(z *vector.Rasterizer) {
		w := float32(tb.Dx())*0.65 + 60
		z.MoveTo(cx-w, fy)
		z.QuadTo(cx, fy-2.5, cx+w, fy)
		z.QuadTo(cx, fy+2.5, cx-w, fy)
		z.ClosePath()
	})
	r.paintColor(blur(flare, 2), withAlpha(hex("#bae6fd"), 0.9), 0, 0)
	fx := cx + float32(tb.Dx())*0.22
	r.paintColor(blur(rasterMask(func(z *vector.Rasterizer) { circle(z, fx, fy, 10) }), 6), withAlpha(hex("#e0f2fe"), 0.95), 0, 0)
	r.paintColor(rasterMask(func(z *vector.Rasterizer) { star(z, fx, fy, 16) }), white, 0, 0)
}

// ---- western: dusty wanted-poster lettering with sheriff stars ----

func (r *renderer) western() {
	tb := r.text.bounds
	// wanted-poster rules above and below
	rules := rasterMask(func(z *vector.Rasterizer) {
		x0, x1 := float32(tb.Min.X)+10, float32(tb.Max.X)-10
		for i, y := range []float32{float32(tb.Min.Y) - 18, float32(tb.Min.Y) - 11, float32(tb.Max.Y) + 11, float32(tb.Max.Y) + 18} {
			th := float32(1.2)
			if i == 0 || i == 3 {
				th = 3
			}
			polygon(z, [][2]float32{{x0, y - th/2}, {x1, y - th/2}, {x1, y + th/2}, {x0, y + th/2}})
		}
	})
	r.paintColor(rules, hex("#d6a565"), 0, 0)
	ow := r.outlineWidth(0.06)
	r.paintColor(blur(dilate(r.text.mask, ow), 4), withAlpha(black, 0.5), 3, 6)
	r.paintColor(dilate(r.text.mask, ow), hex("#3b2414"), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#fff3d6"), hex("#f2c98a"), hex("#c9853e")), 0, 0)
	// worn texture: speckles knocked out of the letters
	worn := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 90; i++ {
			circle(z, float32(tb.Min.X+r.rng.Intn(max(1, tb.Dx()))), float32(tb.Min.Y+r.rng.Intn(max(1, tb.Dy()))), 0.6+float32(r.rng.Float64()*1.6))
		}
	})
	r.paintColor(intersect(worn, r.text.mask), withAlpha(hex("#8a5a2b"), 0.55), 0, 0)
	// sheriff stars left and right
	sy := float32(tb.Min.Y+tb.Max.Y) / 2
	sr := float32(math.Min(26, float64(tb.Dy())*0.3))
	for _, sx := range []float32{float32(tb.Min.X) - sr - 14, float32(tb.Max.X) + sr + 14} {
		if sx-sr < 0 || sx+sr > W {
			continue
		}
		m := rasterMask(func(z *vector.Rasterizer) { star5(z, sx, sy, sr) })
		r.paintColor(dilate(m, 3), hex("#3b2414"), 0, 0)
		r.paint(m, gradient(image.Rect(0, int(sy-sr), W, int(sy+sr)), hex("#fde68a"), hex("#b7791f")), 0, 0)
	}
}

// ---- comic: punchy lettering on an explosion burst with halftone dots ----

func (r *renderer) comic() {
	tb := r.text.bounds
	cx, cy := center(tb)
	rx := float32(tb.Dx())/2 + 46
	ry := float32(tb.Dy())/2 + 40
	burst := rasterMask(func(z *vector.Rasterizer) {
		n := 22
		var pts [][2]float32
		for i := 0; i < n*2; i++ {
			a := float64(i) * math.Pi / float64(n)
			k := float32(1.0)
			if i%2 == 1 {
				k = 0.78 + float32(r.rng.Float64()*0.06)
			} else {
				k = 1.0 + float32(r.rng.Float64()*0.12)
			}
			pts = append(pts, [2]float32{cx + rx*k*float32(math.Cos(a)), cy + ry*k*float32(math.Sin(a))})
		}
		polygon(z, pts)
	})
	r.paintColor(burst, hex("#111111"), 6, 7)
	r.paintColor(dilate(burst, 6), hex("#111111"), 0, 0)
	r.paint(burst, gradient(image.Rect(0, int(cy-ry), W, int(cy+ry)), hex("#ff3b30"), hex("#ff7a00")), 0, 0)
	dots := rasterMask(func(z *vector.Rasterizer) {
		for y := 0; y < H; y += 11 {
			off := (y / 11 % 2) * 5
			for x := off; x < W; x += 11 {
				d := math.Hypot(float64(float32(x)-cx)/float64(rx), float64(float32(y)-cy)/float64(ry))
				rad := float32(math.Max(0, (d-0.35)*4.2))
				if rad > 0.4 {
					circle(z, float32(x), float32(y), rad)
				}
			}
		}
	})
	r.paintColor(intersect(dots, burst), withAlpha(hex("#ffd60a"), 0.55), 0, 0)
	ow := r.outlineWidth(0.06)
	r.paintColor(dilate(r.text.mask, ow), hex("#111111"), ow/2+3, ow/2+3)
	r.paintColor(dilate(r.text.mask, ow), hex("#111111"), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#fff7ae"), hex("#ffd60a"), hex("#ffb000")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.35), white, -1, -2)
}

// ---- crime: typewriter text on yellow police tape ----

func (r *renderer) crime() {
	tb := r.text.bounds
	cx, cy := center(tb)
	h := float32(tb.Dy()) + 44
	angle := -0.07
	rot := func(x, y float32) [2]float32 {
		dx, dy := float64(x-cx), float64(y-cy)
		return [2]float32{cx + float32(dx*math.Cos(angle)-dy*math.Sin(angle)), cy + float32(dx*math.Sin(angle)+dy*math.Cos(angle))}
	}
	rect := func(x0, y0, x1, y1 float32) [][2]float32 {
		return [][2]float32{rot(x0, y0), rot(x1, y0), rot(x1, y1), rot(x0, y1)}
	}
	// a second tape crossing behind
	angle = 0.12
	back := rasterMask(func(z *vector.Rasterizer) { polygon(z, rect(-80, cy-h*0.28, W+80, cy+h*0.28)) })
	r.paintColor(blur(back, 5), withAlpha(black, 0.4), 0, 5)
	r.paintColor(back, hex("#d9b600"), 0, 0)
	angle = -0.07
	tape := rasterMask(func(z *vector.Rasterizer) { polygon(z, rect(-80, cy-h/2, W+80, cy+h/2)) })
	r.paintColor(blur(tape, 6), withAlpha(black, 0.5), 0, 6)
	r.paint(tape, gradient(image.Rect(0, int(cy-h/2), W, int(cy+h/2)), hex("#ffe14d"), hex("#ffd000"), hex("#f2c200")), 0, 0)
	edges := rasterMask(func(z *vector.Rasterizer) {
		polygon(z, rect(-80, cy-h/2+5, W+80, cy-h/2+9))
		polygon(z, rect(-80, cy+h/2-9, W+80, cy+h/2-5))
	})
	r.paintColor(edges, hex("#111111"), 0, 0)
	// hazard stripes at both ends of the tape
	stripes := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 6; i++ {
			for _, base := range []float32{float32(tb.Min.X) - 24 - 96, float32(tb.Max.X) + 30} {
				x := base + float32(i)*16
				polygon(z, [][2]float32{rot(x, cy-h/2+10), rot(x+8, cy-h/2+10), rot(x-6, cy+h/2-10), rot(x-14, cy+h/2-10)})
			}
		}
	})
	r.paintColor(intersect(subtract(stripes, dilate(r.text.mask, 14)), tape), hex("#111111"), 0, 0)
	r.paintColor(r.text.mask, hex("#121212"), 0, 0)
}

// ---- gothic: silver blackletter, blood-red halo and bats ----

func (r *renderer) gothic() {
	tb := r.text.bounds
	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(10, r.text.size*0.16))), 1.5), withAlpha(hex("#7f0010"), 0.9), 0, 0)
	ow := r.outlineWidth(0.04)
	r.paintColor(dilate(r.text.mask, ow), hex("#0a0a0f"), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#ffffff"), hex("#c7cad6"), hex("#7b8094"), hex("#d9dbe4")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.3), white, 0, -1)
	// a full moon peeking out behind and a small colony of bats
	mx := float32(tb.Max.X) - float32(tb.Dx())*0.12
	my := float32(tb.Min.Y) - 8
	moon := rasterMask(func(z *vector.Rasterizer) { circle(z, mx, my, 30) })
	moon = subtract(moon, dilate(r.text.mask, ow+2))
	r.paintColor(blur(moon, 8), withAlpha(hex("#fde68a"), 0.35), 0, 0)
	r.paintColor(moon, withAlpha(hex("#fef3c7"), 0.85), 0, 0)
	bats := rasterMask(func(z *vector.Rasterizer) {
		spots := [][3]float32{{mx - 34, my - 4, 30}, {mx + 30, my - 24, 22}, {mx - 80, my - 30, 18}, {float32(tb.Min.X) + 30, my - 6, 20}}
		for _, b := range spots {
			y := b[1]
			if y < b[2]*0.5+4 {
				y = b[2]*0.5 + 4
			}
			bat(z, b[0], y, b[2])
		}
	})
	r.paintColor(bats, hex("#0a0a0f"), 0, 0)
	r.paintColor(subtract(dilate(bats, 2), bats), withAlpha(hex("#7f0010"), 0.6), 0, 0)
}

// lineBands returns the vertical [start, end) row ranges that contain ink, one per text line.
func lineBands(a *image.Alpha) [][2]int {
	var bands [][2]int
	start := -1
	for y := 0; y < H; y++ {
		ink := false
		for _, v := range a.Pix[y*a.Stride : y*a.Stride+W] {
			if v > 40 {
				ink = true
				break
			}
		}
		if ink && start < 0 {
			start = y
		}
		if !ink && start >= 0 {
			if y-start > 6 {
				bands = append(bands, [2]int{start, y})
			}
			start = -1
		}
	}
	if start >= 0 {
		bands = append(bands, [2]int{start, H})
	}
	return bands
}
