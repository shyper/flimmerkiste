package logo

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"
)

var (
	white = color.RGBA{255, 255, 255, 255}
	black = color.RGBA{0, 0, 0, 255}
)

// classic: bold condensed caps on a glossy colour badge.
func (r *renderer) classic() {
	tb := r.text.bounds
	badge := image.Rect(tb.Min.X-36, tb.Min.Y-26, tb.Max.X+36, tb.Max.Y+26).Intersect(image.Rect(4, 4, W-4, H-4))
	bm := roundedRect(badge, 34)
	r.paintColor(blur(bm, 10), withAlpha(black, 0.45), 0, 8)
	r.paint(bm, gradient(badge, mix(r.accent, white, 0.18), r.accent, mix(r.accent, black, 0.35)), 0, 0)
	// glossy highlight over the upper half
	hl := roundedRect(image.Rect(badge.Min.X+6, badge.Min.Y+6, badge.Max.X-6, badge.Min.Y+badge.Dy()/2), 28)
	r.paintColor(hl, withAlpha(white, 0.12), 0, 0)
	r.paintColor(blur(r.text.mask, 4), withAlpha(black, 0.5), 0, 4)
	r.paintColor(r.text.mask, white, 0, 0)
}

// retro: 80s chrome sunset with horizontal cut stripes and a hard drop shadow.
func (r *renderer) retro() {
	tb := r.text.bounds
	ow := r.outlineWidth(0.09)
	shadow := dilate(r.text.mask, ow)
	r.paintColor(shadow, hex("#1a0b3d"), ow, ow)
	r.paintColor(shadow, hex("#2d1170"), 0, 0)
	cut := cloneAlpha(r.text.mask)
	start := tb.Min.Y + tb.Dy()*55/100
	for y := start; y < tb.Max.Y; y++ {
		p := float64(y-start) / float64(tb.Max.Y-start)
		period := 12
		gap := int(2 + p*7)
		if (y-start)%period < gap {
			for x := 0; x < W; x++ {
				cut.Pix[y*cut.Stride+x] = 0
			}
		}
	}
	r.paint(cut, gradient(tb, hex("#fff6a8"), hex("#ffb347"), hex("#ff4f8b"), hex("#b23cff")), 0, 0)
	// thin highlight line along the top
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.25), white, 0, -2)
}

// neon: white-hot tube core with a wide coloured glow.
func (r *renderer) neon() {
	glowWide := blur(r.text.mask, int(math.Max(10, r.text.size*0.16)))
	glowNear := blur(r.text.mask, int(math.Max(4, r.text.size*0.05)))
	r.paintColor(scaleAlpha(glowWide, 1.6), r.accent, 0, 0)
	r.paintColor(scaleAlpha(glowNear, 1.8), mix(r.accent, white, 0.2), 0, 0)
	r.paintColor(r.text.mask, mix(r.accent, white, 0.82), 0, 0)
}

// cinema: gold serif on a black plate with art-deco rules.
func (r *renderer) cinema() {
	tb := r.text.bounds
	gold := gradient(tb, hex("#fff3c4"), hex("#e5b94e"), hex("#a8741a"), hex("#f6d77a"))
	plate := image.Rect(tb.Min.X-44, tb.Min.Y-44, tb.Max.X+44, tb.Max.Y+44).Intersect(image.Rect(4, 4, W-4, H-4))
	pm := roundedRect(plate, 18)
	r.paintColor(blur(pm, 8), withAlpha(black, 0.5), 0, 6)
	r.paintColor(pm, hex("#0d0d10"), 0, 0)
	inner := roundedRect(image.Rect(plate.Min.X+8, plate.Min.Y+8, plate.Max.X-8, plate.Max.Y-8), 12)
	border := cloneAlpha(pm)
	for i, v := range inner.Pix {
		border.Pix[i] = uint8(clamp(int(border.Pix[i])-int(v), 0, 255))
	}
	inner2 := roundedRect(image.Rect(plate.Min.X+11, plate.Min.Y+11, plate.Max.X-11, plate.Max.Y-11), 10)
	ring := cloneAlpha(inner)
	for i, v := range inner2.Pix {
		ring.Pix[i] = uint8(clamp(int(ring.Pix[i])-int(v), 0, 255))
	}
	r.paint(ring, gradient(plate, hex("#f6d77a"), hex("#a8741a")), 0, 0)
	// deco rules with a diamond above and below the title
	cx := float32(W / 2)
	for _, y := range []float32{float32(tb.Min.Y) - 22, float32(tb.Max.Y) + 22} {
		m := rasterMask(func(z *vector.Rasterizer) {
			half := float32(tb.Dx()) * 0.32
			z.MoveTo(cx-half, y-1.2)
			z.LineTo(cx-14, y-1.2)
			z.LineTo(cx-14, y+1.2)
			z.LineTo(cx-half, y+1.2)
			z.ClosePath()
			z.MoveTo(cx+14, y-1.2)
			z.LineTo(cx+half, y-1.2)
			z.LineTo(cx+half, y+1.2)
			z.LineTo(cx+14, y+1.2)
			z.ClosePath()
			z.MoveTo(cx, y-7)
			z.LineTo(cx+7, y)
			z.LineTo(cx, y+7)
			z.LineTo(cx-7, y)
			z.ClosePath()
		})
		r.paint(m, gold, 0, 0)
	}
	r.paintColor(blur(r.text.mask, 3), withAlpha(black, 0.8), 0, 3)
	r.paint(r.text.mask, gold, 0, 0)
}

// cozy: friendly script with a long stepped 3D shadow.
func (r *renderer) cozy() {
	ow := r.outlineWidth(0.07)
	outline := dilate(r.text.mask, ow)
	depth := int(math.Max(6, r.text.size*0.1))
	dark := mix(r.accent, black, 0.45)
	for i := depth; i >= 1; i-- {
		r.paintColor(outline, dark, i, i)
	}
	r.paintColor(outline, r.accent, 0, 0)
	r.paint(r.text.mask, gradient(r.text.bounds, white, hex("#fff1dc")), 0, 0)
}

// elegant: flowing golden script with a soft dark halo for legibility.
func (r *renderer) elegant() {
	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(6, r.text.size*0.08))), 1.3), withAlpha(hex("#1b1024"), 0.85), 0, 2)
	r.paint(r.text.mask, gradient(r.text.bounds, hex("#fffbe8"), hex("#f1cf7a"), hex("#c98f2c"), hex("#f6dc8f")), 0, 0)
	// a few sparkles on the swashes
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 3; i++ {
			g := r.text.glyphs[r.rng.Intn(len(r.text.glyphs))]
			x := float32(g.x0 + r.rng.Intn(max(1, g.x1-g.x0)))
			y := float32(g.y0 + r.rng.Intn(max(1, (g.y1-g.y0)/2)))
			star(z, x, y, 10+float32(r.rng.Intn(8)))
		}
	}), white, 0, 0)
}

// horror: dripping blood-red letters with a sickly dark glow.
func (r *renderer) horror() {
	mask := cloneAlpha(r.text.mask)
	// Drips: find the lowest ink pixel in random columns and hang a drop below it.
	drips := 4 + len(r.text.glyphs)/2
	for i := 0; i < drips; i++ {
		g := r.text.glyphs[r.rng.Intn(len(r.text.glyphs))]
		if g.x1-g.x0 < 6 {
			continue
		}
		x := g.x0 + 3 + r.rng.Intn(g.x1-g.x0-5)
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
		w := float32(3 + r.rng.Intn(int(math.Max(3, r.text.size*0.06))))
		length := float32(12 + r.rng.Intn(int(math.Max(14, r.text.size*0.45))))
		fx, fy := float32(x), float32(bottom-2)
		union(mask, rasterMask(func(z *vector.Rasterizer) {
			z.MoveTo(fx-w/2, fy)
			z.LineTo(fx+w/2, fy)
			z.LineTo(fx+w/2*0.8, fy+length)
			z.LineTo(fx-w/2*0.8, fy+length)
			z.ClosePath()
			circle(z, fx, fy+length, w*0.85)
		}))
	}
	r.paintColor(scaleAlpha(blur(mask, int(math.Max(8, r.text.size*0.12))), 1.4), withAlpha(hex("#2a0000"), 0.9), 0, 0)
	r.paintColor(scaleAlpha(blur(mask, 4), 0.9), withAlpha(hex("#5aff3c"), 0.18), 0, 0)
	r.paint(mask, gradient(r.text.bounds, hex("#ff3b3b"), hex("#b30000"), hex("#5c0000")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.2), white, -1, -2)
}

// kids: every letter in a different candy colour with a fat double outline.
func (r *renderer) kids() {
	ow := r.outlineWidth(0.08)
	r.paintColor(blur(dilate(r.text.mask, ow*2), 4), withAlpha(black, 0.35), 0, ow)
	r.paintColor(dilate(r.text.mask, ow*2), hex("#1e2a78"), 0, 0)
	r.paintColor(dilate(r.text.mask, ow), white, 0, 0)
	palette := []color.RGBA{hex("#ff4d4d"), hex("#ffb000"), hex("#ffe14d"), hex("#3ddc84"), hex("#2ec5ff"), hex("#7b6cff"), hex("#ff6bd6")}
	fill := image.NewRGBA(image.Rect(0, 0, W, H))
	offset := r.rng.Intn(len(palette))
	for i, g := range r.text.glyphs {
		c := palette[(i+offset)%len(palette)]
		top := mix(c, white, 0.25)
		for y := max(0, g.y0-20); y < min(H, g.y1+20); y++ {
			t := float64(y-g.y0) / float64(max(1, g.y1-g.y0))
			col := mix(top, c, math.Max(0, math.Min(1, t)))
			for x := max(0, g.x0-2); x < min(W, g.x1+2); x++ {
				fill.SetRGBA(x, y, col)
			}
		}
	}
	r.paint(r.text.mask, fill, 0, 0)
	// shine dot on the upper part of each letter
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.18), white, -2, -3)
}

// festive: red and gold with snow dust and twinkling stars.
func (r *renderer) festive() {
	snow := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 40; i++ {
			circle(z, float32(r.rng.Intn(W)), float32(r.rng.Intn(H)), 1.5+float32(r.rng.Float64()*3))
		}
	})
	r.paintColor(snow, withAlpha(white, 0.55), 0, 0)
	ow := r.outlineWidth(0.06)
	gold := gradient(r.text.bounds, hex("#fff2b0"), hex("#e8b33a"), hex("#b9801d"))
	r.paintColor(blur(dilate(r.text.mask, ow), 6), withAlpha(black, 0.5), 0, 5)
	r.paint(dilate(r.text.mask, ow), gold, 0, 0)
	r.paint(r.text.mask, gradient(r.text.bounds, hex("#ff5b5b"), hex("#d0101e"), hex("#8e0612")), 0, 0)
	type sparkle struct{ x, y, size float32 }
	var sparkles []sparkle
	for i := 0; i < 5; i++ {
		g := r.text.glyphs[r.rng.Intn(len(r.text.glyphs))]
		sparkles = append(sparkles, sparkle{
			float32(g.x0 + r.rng.Intn(max(1, g.x1-g.x0))),
			float32(g.y0 - 4 + r.rng.Intn(max(1, (g.y1-g.y0)/3))),
			9 + float32(r.rng.Intn(10)),
		})
	}
	halo := rasterMask(func(z *vector.Rasterizer) {
		for _, sp := range sparkles {
			star(z, sp.x, sp.y, sp.size*1.6)
		}
	})
	r.paintColor(blur(halo, 3), withAlpha(hex("#fff2b0"), 0.7), 0, 0)
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for _, sp := range sparkles {
			star(z, sp.x, sp.y, sp.size)
		}
	}), white, 0, 0)
}

// spring: pastel candy gradient, white outline and little blossoms.
func (r *renderer) spring() {
	blossoms := []color.RGBA{hex("#ffb3d1"), hex("#ffe58a"), hex("#b8f2c9"), hex("#c7b8ff")}
	type flower struct {
		x, y, size float32
		c          int
	}
	var flowers []flower
	tb := r.text.bounds.Inset(-14)
	for tries := 0; len(flowers) < 9 && tries < 200; tries++ {
		x, y := float32(24+r.rng.Intn(W-48)), float32(20+r.rng.Intn(H-40))
		if image.Pt(int(x), int(y)).In(tb) {
			continue
		}
		flowers = append(flowers, flower{x, y, 7 + float32(r.rng.Intn(9)), r.rng.Intn(len(blossoms))})
	}
	// one mask per petal colour, one for all centres
	for ci, c := range blossoms {
		m := rasterMask(func(z *vector.Rasterizer) {
			for _, f := range flowers {
				if f.c != ci {
					continue
				}
				for p := 0; p < 5; p++ {
					a := float64(p) * 2 * math.Pi / 5
					circle(z, f.x+f.size*float32(math.Cos(a)), f.y+f.size*float32(math.Sin(a)), f.size*0.62)
				}
			}
		})
		r.paintColor(m, withAlpha(c, 0.85), 0, 0)
	}
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		for _, f := range flowers {
			circle(z, f.x, f.y, f.size*0.45)
		}
	}), hex("#ffd23f"), 0, 0)
	ow := r.outlineWidth(0.07)
	r.paintColor(blur(dilate(r.text.mask, ow), 5), withAlpha(hex("#6b3d7a"), 0.45), 0, 5)
	r.paintColor(dilate(r.text.mask, ow), white, 0, 0)
	r.paint(r.text.mask, gradient(r.text.bounds, hex("#ff8fc2"), hex("#ffb86b"), hex("#7ad9a6")), 0, 0)
}

// action: fiery gradient, heavy outline and speed lines.
func (r *renderer) action() {
	tb := r.text.bounds
	for i := 0; i < 7; i++ {
		y := float32(tb.Min.Y + r.rng.Intn(max(1, tb.Dy())))
		x1 := float32(tb.Min.X - 8)
		length := float32(40 + r.rng.Intn(110))
		th := float32(2 + r.rng.Intn(4))
		m := rasterMask(func(z *vector.Rasterizer) {
			z.MoveTo(x1-length, y)
			z.LineTo(x1, y-th/2)
			z.LineTo(x1, y+th/2)
			z.ClosePath()
		})
		r.paintColor(m, withAlpha(hex("#ffb000"), 0.85), 0, 0)
	}
	ow := r.outlineWidth(0.06)
	r.paintColor(dilate(r.text.mask, ow), hex("#1a1a1a"), ow/2+3, ow/2+3)
	r.paintColor(dilate(r.text.mask, ow), hex("#111111"), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#fff27a"), hex("#ffb000"), hex("#ff4d00"), hex("#c41200")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.3), white, 0, -2)
}
