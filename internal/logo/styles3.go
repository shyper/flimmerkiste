package logo

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"
)

// heart draws a heart centred at (cx, cy) with the given size.
func heart(z *vector.Rasterizer, cx, cy, s float32) {
	z.MoveTo(cx, cy+s*0.42)
	z.CubeTo(cx-s*1.15, cy-s*0.25, cx-s*0.5, cy-s*1.05, cx, cy-s*0.42)
	z.CubeTo(cx+s*0.5, cy-s*1.05, cx+s*1.15, cy-s*0.25, cx, cy+s*0.42)
	z.ClosePath()
}

func rect(z *vector.Rasterizer, x0, y0, x1, y1 float32) {
	polygon(z, [][2]float32{{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1}})
}

// fillRows paints a mask with one colour per horizontal band (used for arcade stripes).
func bands(rect image.Rectangle, colors ...color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	h := max(1, rect.Dy())
	for y := 0; y < H; y++ {
		i := clamp((y-rect.Min.Y)*len(colors)/h, 0, len(colors)-1)
		for x := 0; x < W; x++ {
			img.SetRGBA(x, y, colors[i])
		}
	}
	return img
}

// fadeEdges softens a mask towards the left/right ends of [x0, x1] and below yFade.
func fadeEdges(m *image.Alpha, x0, x1, fade int, yFade, yEnd int) *image.Alpha {
	out := cloneAlpha(m)
	for y := 0; y < H; y++ {
		vy := 1.0
		if yEnd > yFade && y > yFade {
			vy = math.Max(0, 1-float64(y-yFade)/float64(yEnd-yFade))
		}
		for x := 0; x < W; x++ {
			v := vy
			if d := x - x0; d < fade {
				v *= math.Max(0, float64(d)/float64(fade))
			}
			if d := x1 - x; d < fade {
				v *= math.Max(0, float64(d)/float64(fade))
			}
			i := y*out.Stride + x
			out.Pix[i] = uint8(float64(out.Pix[i]) * v)
		}
	}
	return out
}

// ---- nature: slab lettering above a mountain range at golden hour ----

func (r *renderer) nature() {
	tb := r.text.bounds
	base := float32(tb.Max.Y) + 22
	sunX, sunY := float32(tb.Max.X)+6, float32(tb.Min.Y)+4
	r.paintColor(blur(rasterMask(func(z *vector.Rasterizer) { circle(z, sunX, sunY, 44) }), 16), withAlpha(hex("#fdba74"), 0.45), 0, 0)
	r.paintColor(rasterMask(func(z *vector.Rasterizer) { circle(z, sunX, sunY, 24) }), withAlpha(hex("#fcd34d"), 0.85), 0, 0)
	// two mountain ridges, the back one lighter
	ridge := func(height, jitter float32, seed int) *image.Alpha {
		return rasterMask(func(z *vector.Rasterizer) {
			x0, x1 := float32(tb.Min.X)-40, float32(tb.Max.X)+40
			pts := [][2]float32{{x0, base}}
			n := 7 + seed
			for i := 0; i <= n; i++ {
				x := x0 + (x1-x0)*float32(i)/float32(n)
				y := base - height*(0.35+0.65*float32(r.rng.Float64()))
				if i%2 == 1 {
					y = base - height*0.25*float32(r.rng.Float64()) - jitter
				}
				pts = append(pts, [2]float32{x, y})
			}
			pts = append(pts, [2]float32{x1, base})
			polygon(z, pts)
		})
	}
	back := ridge(float32(tb.Dy())*0.75, 10, 2)
	front := ridge(float32(tb.Dy())*0.45, 0, 0)
	fx0, fx1 := tb.Min.X-40, tb.Max.X+40
	back = fadeEdges(back, fx0, fx1, 70, int(base)-12, int(base))
	front = fadeEdges(front, fx0, fx1, 70, int(base)-12, int(base))
	r.paint(scaleAlpha(back, 0.7), gradient(image.Rect(0, int(base)-tb.Dy(), W, int(base)), hex("#3f5f63"), hex("#263c40")), 0, 0)
	r.paint(scaleAlpha(front, 0.9), gradient(image.Rect(0, int(base)-tb.Dy()/2, W, int(base)), hex("#1d2f2c"), hex("#121c1b")), 0, 0)
	// birds
	birds := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 3; i++ {
			bx := float32(tb.Min.X) + float32(tb.Dx())*(0.12+0.1*float32(i)) + float32(r.rng.Intn(12))
			by := float32(tb.Min.Y) - 14 - float32(r.rng.Intn(16))
			s := 7 + float32(r.rng.Intn(4))
			z.MoveTo(bx-s, by-s*0.3)
			z.QuadTo(bx-s*0.4, by-s*0.7, bx, by)
			z.QuadTo(bx+s*0.4, by-s*0.7, bx+s, by-s*0.3)
			z.QuadTo(bx+s*0.4, by-s*0.45, bx, by+s*0.25)
			z.QuadTo(bx-s*0.4, by-s*0.45, bx-s, by-s*0.3)
			z.ClosePath()
		}
	})
	r.paintColor(birds, hex("#1f2a24"), 0, 0)
	ow := r.outlineWidth(0.05)
	r.paintColor(blur(dilate(r.text.mask, ow), 5), withAlpha(black, 0.5), 0, 4)
	r.paintColor(dilate(r.text.mask, ow), hex("#14281f"), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#fffbeb"), hex("#fde68a"), hex("#a3e635")), 0, 0)
}

// ---- ocean: chunky rounded lettering riding the waves ----

func (r *renderer) ocean() {
	tb := r.text.bounds
	waveTop := float32(tb.Max.Y) - float32(tb.Dy())*0.12
	for i, c := range []string{"#7dd3fc", "#0ea5e9", "#0369a1"} {
		y0 := waveTop + float32(i)*14
		amp := float32(6 + i*2)
		phase := r.rng.Float64() * math.Pi
		m := rasterMask(func(z *vector.Rasterizer) {
			x0, x1 := float32(tb.Min.X)-50, float32(tb.Max.X)+50
			z.MoveTo(x0, y0)
			for x := x0; x <= x1; x += 6 {
				z.LineTo(x, y0+amp*float32(math.Sin(float64(x)/22+phase)))
			}
			z.LineTo(x1, y0+40)
			z.LineTo(x0, y0+40)
			z.ClosePath()
		})
		m = fadeEdges(m, tb.Min.X-50, tb.Max.X+50, 80, int(y0)+10, int(y0)+38)
		r.paintColor(m, withAlpha(hex(c), 0.8), 0, 0)
	}
	ow := r.outlineWidth(0.08)
	r.paintColor(blur(dilate(r.text.mask, ow), 5), withAlpha(hex("#082f49"), 0.6), 0, 6)
	r.paintColor(dilate(r.text.mask, ow), white, 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#e0f7ff"), hex("#38bdf8"), hex("#0369a1")), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.35), white, -1, -2)
	bubbles := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 9; i++ {
			bx := float32(tb.Min.X - 30 + r.rng.Intn(tb.Dx()+60))
			by := float32(tb.Min.Y - 40 + r.rng.Intn(40))
			rad := 3 + float32(r.rng.Intn(6))
			circle(z, bx, by, rad)
		}
	})
	r.paintColor(subtract(dilate(bubbles, 2), bubbles), withAlpha(hex("#bae6fd"), 0.9), 0, 0)
	r.paintColor(bubbles, withAlpha(hex("#e0f2fe"), 0.25), 0, 0)
}

// ---- sport: varsity lettering with a double outline and a row of stars ----

func (r *renderer) sport() {
	tb := r.text.bounds
	ow := r.outlineWidth(0.05)
	r.paintColor(blur(dilate(r.text.mask, ow*2), 4), withAlpha(black, 0.45), 0, 6)
	r.paintColor(dilate(r.text.mask, ow*2), hex("#0f2557"), 0, 0)
	r.paintColor(dilate(r.text.mask, ow), hex("#d61f2c"), 0, 0)
	r.paint(r.text.mask, gradient(tb, white, hex("#f1f1f1"), hex("#d9d9d9")), 0, 0)
	sy := float32(tb.Max.Y) + 26
	stars := rasterMask(func(z *vector.Rasterizer) {
		for i := -1; i <= 1; i++ {
			star5(z, float32(tb.Min.X+tb.Max.X)/2+float32(i)*34, sy, 11)
		}
	})
	r.paintColor(dilate(stars, 2), hex("#0f2557"), 0, 0)
	r.paintColor(stars, hex("#fbbf24"), 0, 0)
}

// ---- music: neon line lettering over an equaliser ----

func (r *renderer) music() {
	tb := r.text.bounds
	bars := rasterMask(func(z *vector.Rasterizer) {
		n := 22
		x0, x1 := float32(tb.Min.X), float32(tb.Max.X)
		w := (x1 - x0) / float32(n)
		base := float32(tb.Max.Y) + 46
		for i := 0; i < n; i++ {
			h := 10 + float32(r.rng.Float64())*float32(math.Max(46, float64(tb.Dy())*0.9))
			bx := x0 + float32(i)*w
			rect(z, bx+w*0.18, base-h, bx+w*0.82, base)
		}
	})
	eq := gradient(image.Rect(0, tb.Max.Y-20, W, tb.Max.Y+46), hex("#22d3ee"), hex("#a855f7"), hex("#ec4899"))
	r.paint(scaleAlpha(blur(bars, 4), 0.8), eq, 0, 0)
	r.paint(scaleAlpha(bars, 0.75), eq, 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(8, r.text.size*0.14))), 1.6), withAlpha(hex("#ec4899"), 0.9), 0, 0)
	r.paintColor(scaleAlpha(blur(r.text.mask, 3), 1.4), withAlpha(hex("#f0abfc"), 0.8), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#fdf4ff"), hex("#f5d0fe"), hex("#ffffff")), 0, 0)
}

// ---- news: lower-third style bar with an accent block ----

func (r *renderer) news() {
	tb := r.text.bounds
	pad := float32(26)
	slant := float32(22)
	x0, x1 := float32(tb.Min.X)-pad, float32(tb.Max.X)+pad
	y0, y1 := float32(tb.Min.Y)-14, float32(tb.Max.Y)+14
	bar := rasterMask(func(z *vector.Rasterizer) {
		polygon(z, [][2]float32{{x0 + slant, y0}, {x1 + slant, y0}, {x1 - slant*0.2, y1}, {x0 - slant*0.2, y1}})
	})
	r.paintColor(blur(bar, 8), withAlpha(black, 0.5), 0, 7)
	r.paint(bar, gradient(image.Rect(0, int(y0), W, int(y1)), hex("#ef3340"), hex("#c8102e"), hex("#9b0a22")), 0, 0)
	r.paintColor(intersect(bar, rasterMask(func(z *vector.Rasterizer) { rect(z, 0, y0, W, y0+(y1-y0)*0.45) })), withAlpha(white, 0.1), 0, 0)
	accent := rasterMask(func(z *vector.Rasterizer) {
		polygon(z, [][2]float32{{x0 - 26 + slant, y0}, {x0 - 6 + slant, y0}, {x0 - 6 - slant*0.2, y1}, {x0 - 26 - slant*0.2, y1}})
		polygon(z, [][2]float32{{x0 - 2 - slant*0.2, y1 + 6}, {x1 - slant*0.2, y1 + 6}, {x1 - slant*0.25, y1 + 12}, {x0 - 2 - slant*0.25, y1 + 12}})
	})
	r.paintColor(accent, hex("#0b2a5b"), 0, 0)
	r.paintColor(blur(r.text.mask, 2), withAlpha(black, 0.35), 0, 2)
	r.paintColor(r.text.mask, white, 0, 0)
}

// ---- love: soft script with floating hearts ----

func (r *renderer) love() {
	tb := r.text.bounds
	hearts := []struct {
		x, y, s float32
		c       string
	}{}
	for tries := 0; len(hearts) < 8 && tries < 200; tries++ {
		x := float32(tb.Min.X - 36 + r.rng.Intn(tb.Dx()+72))
		y := float32(tb.Min.Y - 40 + r.rng.Intn(tb.Dy()+80))
		if image.Pt(int(x), int(y)).In(tb.Inset(-8)) || x < 12 || x > W-12 || y < 12 || y > H-12 {
			continue
		}
		hearts = append(hearts, struct {
			x, y, s float32
			c       string
		}{x, y, 8 + float32(r.rng.Intn(12)), []string{"#fb7185", "#f43f5e", "#fda4af", "#ec4899"}[r.rng.Intn(4)]})
	}
	for _, h := range hearts {
		m := rasterMask(func(z *vector.Rasterizer) { heart(z, h.x, h.y, h.s) })
		r.paintColor(blur(m, 3), withAlpha(hex(h.c), 0.5), 0, 0)
		r.paintColor(m, hex(h.c), 0, 0)
	}
	r.paintColor(scaleAlpha(blur(r.text.mask, int(math.Max(8, r.text.size*0.12))), 1.3), withAlpha(hex("#be123c"), 0.7), 0, 0)
	ow := r.outlineWidth(0.03)
	r.paintColor(dilate(r.text.mask, ow), withAlpha(white, 0.9), 0, 0)
	r.paint(r.text.mask, gradient(tb, hex("#ff8fab"), hex("#f43f5e"), hex("#be123c")), 0, 0)
}

// ---- pixel: arcade lettering, stepped shadow and CRT scanlines ----

func (r *renderer) pixel() {
	tb := r.text.bounds
	step := int(math.Max(3, r.text.size*0.05))
	r.paintColor(r.text.mask, hex("#1e1b4b"), step*2, step*2)
	r.paintColor(r.text.mask, hex("#4338ca"), step, step)
	r.paint(r.text.mask, bands(tb, hex("#fff27a"), hex("#ffd23f"), hex("#ff9f1c"), hex("#ff5d5d")), 0, 0)
	// scanlines across the lettering
	lines := image.NewAlpha(image.Rect(0, 0, W, H))
	for y := 0; y < H; y += 4 {
		for x := 0; x < W; x++ {
			lines.Pix[y*lines.Stride+x] = 90
		}
	}
	r.paintColor(intersect(lines, r.text.mask), black, 0, 0)
	// a few pixel coins
	coins := rasterMask(func(z *vector.Rasterizer) {
		cs := float32(step * 2)
		for i := 0; i < 3; i++ {
			x := float32(tb.Max.X) + 18 + float32(i)*cs*1.8
			y := float32(tb.Min.Y+tb.Max.Y)/2 - cs/2
			if x+cs > W-4 {
				x = float32(tb.Min.X) - 18 - float32(3-i)*cs*1.8
			}
			rect(z, x, y, x+cs, y+cs)
		}
	})
	r.paintColor(coins, hex("#ffd23f"), 0, 0)
	r.paintColor(intersect(coins, rasterMask(func(z *vector.Rasterizer) { rect(z, 0, 0, W, float32(tb.Min.Y+tb.Max.Y)/2-float32(step)/2) })), withAlpha(white, 0.5), 0, 0)
}

// ---- chalk: chalk lettering on a framed school board ----

func (r *renderer) chalk() {
	tb := r.text.bounds
	board := image.Rect(tb.Min.X-46, tb.Min.Y-36, tb.Max.X+46, tb.Max.Y+40).Intersect(image.Rect(6, 6, W-6, H-6))
	frame := roundedRect(board, 10)
	inner := roundedRect(board.Inset(12), 4)
	r.paintColor(blur(frame, 8), withAlpha(black, 0.5), 0, 6)
	r.paint(frame, gradient(board, hex("#a16207"), hex("#713f12"), hex("#854d0e")), 0, 0)
	r.paint(inner, gradient(board, hex("#2f4f3f"), hex("#24402f"), hex("#1d3627")), 0, 0)
	// faint smudges of old chalk
	smudge := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 3; i++ {
			x := float32(board.Min.X+20) + float32(r.rng.Intn(max(1, board.Dx()-40)))
			y := float32(board.Min.Y+20) + float32(r.rng.Intn(max(1, board.Dy()-40)))
			circle(z, x, y, 30+float32(r.rng.Intn(30)))
		}
	})
	r.paintColor(intersect(blur(smudge, 18), inner), withAlpha(white, 0.06), 0, 0)
	// chalk texture: knock random specks out of the letters
	text := cloneAlpha(r.text.mask)
	for i := range text.Pix {
		if text.Pix[i] > 0 && r.rng.Intn(100) < 18 {
			text.Pix[i] = uint8(int(text.Pix[i]) * (30 + r.rng.Intn(50)) / 100)
		}
	}
	r.paintColor(scaleAlpha(blur(r.text.mask, 2), 0.35), white, 0, 0)
	r.paintColor(text, hex("#f8fafc"), 0, 0)
	// chalk piece on the ledge
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		x, y := float32(board.Max.X)-60, float32(board.Max.Y)-9
		rect(z, x, y-4, x+30, y+2)
	}), hex("#f1f5f9"), 0, 0)
}

// ---- ice: frozen lettering with icicles and snowflakes ----

func (r *renderer) ice() {
	tb := r.text.bounds
	mask := cloneAlpha(r.text.mask)
	// icicles hanging from the lower edges of the letters
	for i := 0; i < 6+len(r.text.glyphs); i++ {
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
		w := float32(3 + r.rng.Intn(int(math.Max(3, r.text.size*0.05))))
		l := float32(10 + r.rng.Intn(int(math.Max(12, r.text.size*0.32))))
		fx, fy := float32(x), float32(bottom-1)
		union(mask, rasterMask(func(z *vector.Rasterizer) {
			polygon(z, [][2]float32{{fx - w, fy}, {fx + w, fy}, {fx, fy + l}})
		}))
	}
	r.paintColor(scaleAlpha(blur(mask, int(math.Max(8, r.text.size*0.14))), 1.3), withAlpha(hex("#38bdf8"), 0.55), 0, 0)
	ow := r.outlineWidth(0.04)
	r.paintColor(dilate(mask, ow), hex("#0c4a6e"), 0, 0)
	r.paint(mask, gradient(tb, white, hex("#e0f2fe"), hex("#7dd3fc"), hex("#bae6fd")), 0, 0)
	// frost highlight along the top edge
	r.paintColor(scaleAlpha(blur(r.text.mask, 1), 0.5), white, 0, -2)
	flakes := rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 7; i++ {
			x := float32(r.rng.Intn(W))
			y := float32(r.rng.Intn(H))
			if image.Pt(int(x), int(y)).In(tb.Inset(-6)) {
				continue
			}
			s := 5 + float32(r.rng.Intn(7))
			for k := 0; k < 3; k++ {
				a := float64(k) * math.Pi / 3
				dx, dy := s*float32(math.Cos(a)), s*float32(math.Sin(a))
				nx, ny := -dy/s*1.1, dx/s*1.1
				polygon(z, [][2]float32{{x - dx + nx, y - dy + ny}, {x + dx + nx, y + dy + ny}, {x + dx - nx, y + dy - ny}, {x - dx - nx, y - dy - ny}})
			}
		}
	})
	r.paintColor(flakes, withAlpha(white, 0.85), 0, 0)
}
