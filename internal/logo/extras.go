package logo

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

var styleFuncs = map[string]func(*renderer){
	"classic": (*renderer).classic, "retro": (*renderer).retro, "neon": (*renderer).neon,
	"cinema": (*renderer).cinema, "cozy": (*renderer).cozy, "elegant": (*renderer).elegant,
	"horror": (*renderer).horror, "kids": (*renderer).kids, "festive": (*renderer).festive,
	"spring": (*renderer).spring, "action": (*renderer).action,
	"magic": (*renderer).magic, "epic": (*renderer).epic, "fairytale": (*renderer).fairytale,
	"space": (*renderer).space, "western": (*renderer).western, "comic": (*renderer).comic,
	"crime": (*renderer).crime, "gothic": (*renderer).gothic,
	"nature": (*renderer).nature, "ocean": (*renderer).ocean, "sport": (*renderer).sport,
	"music": (*renderer).music, "news": (*renderer).news, "love": (*renderer).love,
	"pixel": (*renderer).pixel, "chalk": (*renderer).chalk, "ice": (*renderer).ice,
	"anime": (*renderer).anime, "silent": (*renderer).silent, "crayon": (*renderer).crayon,
	"mystery": (*renderer).mystery, "bollywood": (*renderer).bollywood, "cooking": (*renderer).cooking,
	"atomic": (*renderer).atomic, "graffiti": (*renderer).graffiti, "manuscript": (*renderer).manuscript,
	"vhs": (*renderer).vhs,
}

// ---- colour theme: hue rotation ----

// rotateHue turns the hue of every coloured pixel; greys, black and white stay as they are.
func rotateHue(img *image.RGBA, deg float64) {
	for i := 0; i < len(img.Pix); i += 4 {
		a := img.Pix[i+3]
		if a == 0 {
			continue
		}
		// un-premultiply
		af := float64(a) / 255
		r, g, b := float64(img.Pix[i])/255/af, float64(img.Pix[i+1])/255/af, float64(img.Pix[i+2])/255/af
		h, s, v := rgbToHSV(r, g, b)
		if s < 0.05 {
			continue
		}
		h = math.Mod(h+deg+360, 360)
		r, g, b = hsvToRGB(h, s, v)
		img.Pix[i] = uint8(math.Min(1, r) * af * 255)
		img.Pix[i+1] = uint8(math.Min(1, g) * af * 255)
		img.Pix[i+2] = uint8(math.Min(1, b) * af * 255)
	}
}

func rgbToHSV(r, g, b float64) (h, s, v float64) {
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	v = mx
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return
}

func hsvToRGB(h, s, v float64) (float64, float64, float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

// ---- tagline and number badge ----

const taglineH = 46

func faceFor(file string, size float64) (font.Face, error) {
	f, err := loadFont(file)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
}

// tagline writes a small spaced-out line below the name.
func (r *renderer) tagline(text string, centerY float64) error {
	text = strings.ToUpper(strings.TrimSpace(text))
	if text == "" {
		return nil
	}
	size := 30.0
	var face font.Face
	var spaced string
	for ; size >= 16; size -= 2 {
		f, err := faceFor("BebasNeue-Regular.ttf", size)
		if err != nil {
			return err
		}
		spaced = strings.Join(strings.Split(text, ""), " ")
		if measure(f, spaced)+float64(len([]rune(text)))*size*0.08 < float64(W-60) {
			face = f
			break
		}
		f.Close()
	}
	if face == nil {
		return nil
	}
	defer face.Close()
	// fixed slot at the bottom (the name was laid out above it), so it never collides with badges or signs
	// close below the name, but clear of badges/signs drawn around it, and never off-canvas
	_ = centerY
	y := math.Min(float64(r.text.bounds.Max.Y)+34, float64(H-taglineH)-4)
	m := image.NewAlpha(image.Rect(0, 0, W, H))
	d := &font.Drawer{Dst: m, Src: image.Opaque, Face: face}
	runes := []rune(text)
	track := size * 0.08
	total := 0.0
	for _, ch := range runes {
		total += measure(face, string(ch)) + track
	}
	x := (float64(W) - total + track) / 2
	for _, ch := range runes {
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6((y + size*0.7) * 64)}
		d.DrawString(string(ch))
		x += measure(face, string(ch)) + track
	}
	r.paintColor(blur(m, 3), withAlpha(black, 0.7), 0, 2)
	r.paintColor(m, color.RGBA{240, 240, 245, 255}, 0, 0)
	return nil
}

// numberBadge draws the channel number in a small rounded plate in the top-left corner.
func (r *renderer) numberBadge(n int) error {
	label := strconv.Itoa(n)
	face, err := faceFor("BebasNeue-Regular.ttf", 40)
	if err != nil {
		return err
	}
	defer face.Close()
	w := int(measure(face, label)) + 28
	plate := image.Rect(14, 14, 14+max(w, 48), 62)
	m := roundedRect(plate, 12)
	r.paintColor(blur(m, 5), withAlpha(black, 0.55), 0, 3)
	r.paint(m, gradient(plate, mix(r.accent, white, 0.2), mix(r.accent, black, 0.25)), 0, 0)
	txt := image.NewAlpha(image.Rect(0, 0, W, H))
	d := &font.Drawer{Dst: txt, Src: image.Opaque, Face: face}
	x := float64(plate.Min.X) + (float64(plate.Dx())-measure(face, label))/2
	d.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(float64(plate.Max.Y-10) * 64)}
	d.DrawString(label)
	r.paintColor(txt, white, 0, 0)
	return nil
}

// ---- holiday accessories ----

// HolidayAt returns the holiday decoration that fits a date ("" if none).
func HolidayAt(t time.Time) string {
	m, d := t.Month(), t.Day()
	switch {
	case m == time.December && d >= 1 && d <= 26:
		return "christmas"
	case (m == time.December && d >= 30) || (m == time.January && d <= 1):
		return "newyear"
	case m == time.February && d >= 12 && d <= 14:
		return "valentine"
	case (m == time.October && d >= 24) || (m == time.November && d == 1):
		return "halloween"
	}
	easter := EasterSunday(t.Year())
	day := time.Date(t.Year(), m, d, 0, 0, 0, 0, time.UTC)
	if diff := day.Sub(easter).Hours() / 24; diff >= -7 && diff <= 1 {
		return "easter"
	}
	return ""
}

// EasterSunday computes the Gregorian Easter date (anonymous Gregorian algorithm).
func EasterSunday(year int) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func (r *renderer) holiday(kind string) {
	if len(r.text.glyphs) == 0 {
		return
	}
	tb := r.text.bounds
	first := r.text.glyphs[0]
	last := r.text.glyphs[len(r.text.glyphs)-1]
	// accessories scale with the lettering and sit at the top-right of the last letter, inside the canvas
	s := float32(math.Max(26, math.Min(52, r.text.size*0.42)))
	ax := float32(math.Min(float64(last.x1)+float64(s)*0.55, float64(W)-float64(s)-6))
	ay := float32(math.Max(float64(last.y0)+float64(s)*0.55, float64(s)+6))
	switch kind {
	case "christmas":
		r.santaHat(first)
	case "halloween":
		r.pumpkin(ax, ay, s)
	case "easter":
		r.easterEgg(ax, ay, s*0.85)
	case "valentine":
		m := rasterMask(func(z *vector.Rasterizer) { heart(z, ax, ay, s*0.8) })
		r.paintColor(blur(m, 3), withAlpha(black, 0.4), 0, 3)
		r.paintColor(dilate(m, 3), white, 0, 0)
		r.paintColor(m, hex("#e11d48"), 0, 0)
	case "newyear":
		colors := []string{"#f43f5e", "#facc15", "#22d3ee", "#a3e635", "#c084fc", "#fb923c"}
		for i := 0; i < 36; i++ {
			x := float32(tb.Min.X - 40 + r.rng.Intn(tb.Dx()+80))
			y := float32(tb.Min.Y - 50 + r.rng.Intn(50))
			if i%3 == 0 {
				y = float32(tb.Max.Y + r.rng.Intn(40))
			}
			a := r.rng.Float64() * math.Pi
			w, h := float32(3+r.rng.Intn(3)), float32(7+r.rng.Intn(5))
			ca, sa := float32(math.Cos(a)), float32(math.Sin(a))
			pt := func(dx, dy float32) [2]float32 { return [2]float32{x + dx*ca - dy*sa, y + dx*sa + dy*ca} }
			m := rasterMask(func(z *vector.Rasterizer) { polygon(z, [][2]float32{pt(-w, -h), pt(w, -h), pt(w, h), pt(-w, h)}) })
			r.paintColor(m, hex(colors[i%len(colors)]), 0, 0)
		}
	}
}

// santaHat sits tilted on the first letter.
func (r *renderer) santaHat(g glyphSpan) {
	gw := float32(g.x1 - g.x0)
	s := float32(math.Max(40, math.Max(float64(gw)*1.15, float64(g.y1-g.y0)*0.6)))
	// top of the first letter's ink
	top := g.y0
	for y := max(0, g.y0); y < min(H, g.y1); y++ {
		ink := false
		for x := max(0, g.x0); x < min(W, g.x1); x++ {
			if r.text.mask.Pix[y*r.text.mask.Stride+x] > 120 {
				ink = true
				break
			}
		}
		if ink {
			top = y
			break
		}
	}
	cx, by := float32(g.x0)+gw*0.5, float32(top)-s*0.06
	hat := rasterMask(func(z *vector.Rasterizer) {
		z.MoveTo(cx-s*0.55, by)
		z.QuadTo(cx-s*0.2, by-s*0.9, cx+s*0.55, by-s*0.85)
		z.QuadTo(cx+s*0.3, by-s*0.45, cx+s*0.5, by)
		z.ClosePath()
	})
	r.paintColor(blur(hat, 3), withAlpha(black, 0.4), 0, 3)
	r.paint(hat, gradient(image.Rect(0, int(by-s), W, int(by)), hex("#ef4444"), hex("#b91c1c")), 0, 0)
	fur := rasterMask(func(z *vector.Rasterizer) {
		rh := s * 0.16
		z.MoveTo(cx-s*0.62, by-rh)
		z.LineTo(cx+s*0.58, by-rh)
		z.QuadTo(cx+s*0.66, by, cx+s*0.58, by+rh)
		z.LineTo(cx-s*0.62, by+rh)
		z.QuadTo(cx-s*0.7, by, cx-s*0.62, by-rh)
		z.ClosePath()
		circle(z, cx+s*0.58, by-s*0.85, s*0.15)
	})
	r.paintColor(fur, hex("#f8fafc"), 0, 0)
}

func (r *renderer) pumpkin(cx, cy, s float32) {
	body := rasterMask(func(z *vector.Rasterizer) {
		for _, off := range []float32{-0.45, 0, 0.45} {
			z.MoveTo(cx+off*s+s*0.55, cy)
			z.CubeTo(cx+off*s+s*0.55, cy+s*0.75, cx+off*s-s*0.55, cy+s*0.75, cx+off*s-s*0.55, cy)
			z.CubeTo(cx+off*s-s*0.55, cy-s*0.75, cx+off*s+s*0.55, cy-s*0.75, cx+off*s+s*0.55, cy)
			z.ClosePath()
		}
	})
	r.paintColor(blur(body, 3), withAlpha(black, 0.4), 0, 3)
	r.paint(body, gradient(image.Rect(0, int(cy-s), W, int(cy+s)), hex("#fdba74"), hex("#f97316"), hex("#c2410c")), 0, 0)
	r.paintColor(rasterMask(func(z *vector.Rasterizer) {
		rect(z, cx-s*0.08, cy-s*0.85, cx+s*0.1, cy-s*0.5)
	}), hex("#3f6212"), 0, 0)
	face := rasterMask(func(z *vector.Rasterizer) {
		polygon(z, [][2]float32{{cx - s*0.42, cy - s*0.05}, {cx - s*0.18, cy - s*0.05}, {cx - s*0.3, cy - s*0.3}})
		polygon(z, [][2]float32{{cx + s*0.18, cy - s*0.05}, {cx + s*0.42, cy - s*0.05}, {cx + s*0.3, cy - s*0.3}})
		z.MoveTo(cx-s*0.45, cy+s*0.15)
		z.QuadTo(cx, cy+s*0.6, cx+s*0.45, cy+s*0.15)
		z.QuadTo(cx, cy+s*0.35, cx-s*0.45, cy+s*0.15)
		z.ClosePath()
	})
	r.paintColor(face, hex("#fde047"), 0, 0)
}

func (r *renderer) easterEgg(cx, cy, s float32) {
	egg := rasterMask(func(z *vector.Rasterizer) {
		z.MoveTo(cx, cy-s)
		z.CubeTo(cx+s*0.75, cy-s, cx+s*0.8, cy+s*0.85, cx, cy+s*0.85)
		z.CubeTo(cx-s*0.8, cy+s*0.85, cx-s*0.75, cy-s, cx, cy-s)
		z.ClosePath()
	})
	r.paintColor(blur(egg, 3), withAlpha(black, 0.4), 0, 3)
	r.paint(egg, gradient(image.Rect(0, int(cy-s), W, int(cy+s)), hex("#a5f3fc"), hex("#c4b5fd")), 0, 0)
	stripes := rasterMask(func(z *vector.Rasterizer) {
		for _, y := range []float32{cy - s*0.25, cy + s*0.25} {
			z.MoveTo(cx-s, y)
			for k := 0; k <= 8; k++ {
				dy := float32(-4)
				if k%2 == 1 {
					dy = 4
				}
				z.LineTo(cx-s+float32(k)*s/4, y+dy)
			}
			z.LineTo(cx+s, y+8)
			for k := 8; k >= 0; k-- {
				dy := float32(4)
				if k%2 == 1 {
					dy = 12
				}
				z.LineTo(cx-s+float32(k)*s/4, y+dy)
			}
			z.ClosePath()
		}
	})
	r.paintColor(intersect(stripes, egg), hex("#f472b6"), 0, 0)
	r.paintColor(intersect(rasterMask(func(z *vector.Rasterizer) {
		for i := 0; i < 5; i++ {
			circle(z, cx-s*0.4+float32(i)*s*0.2, cy+s*0.55, 2.2)
		}
	}), egg), hex("#facc15"), 0, 0)
}

// ---- dominant colour of posters ----

// DominantColor returns a vivid colour that represents a set of images (e.g. posters).
func DominantColor(imgs []image.Image) (string, bool) {
	var hist [36]float64
	var sum [36][3]float64
	for _, img := range imgs {
		b := img.Bounds()
		stepX, stepY := max(1, b.Dx()/40), max(1, b.Dy()/60)
		for y := b.Min.Y; y < b.Max.Y; y += stepY {
			for x := b.Min.X; x < b.Max.X; x += stepX {
				cr, cg, cb, _ := img.At(x, y).RGBA()
				r, g, bl := float64(cr)/65535, float64(cg)/65535, float64(cb)/65535
				h, s, v := rgbToHSV(r, g, bl)
				if s < 0.25 || v < 0.25 {
					continue
				}
				w := s * v
				i := int(h/10) % 36
				hist[i] += w
				sum[i][0] += r * w
				sum[i][1] += g * w
				sum[i][2] += bl * w
			}
		}
	}
	best, bestW := -1, 0.0
	for i := range hist {
		// smooth over neighbouring buckets
		w := hist[i] + 0.5*(hist[(i+35)%36]+hist[(i+1)%36])
		if w > bestW {
			best, bestW = i, w
		}
	}
	if best < 0 || hist[best] == 0 {
		return "", false
	}
	r, g, b := sum[best][0]/hist[best], sum[best][1]/hist[best], sum[best][2]/hist[best]
	// make it a bit brighter and more saturated so it works as an accent
	h, s, v := rgbToHSV(r, g, b)
	r, g, b = hsvToRGB(h, math.Min(1, s*1.15), math.Min(1, math.Max(v, 0.75)))
	return "#" + hex2(r) + hex2(g) + hex2(b), true
}

func hex2(f float64) string {
	s := strconv.FormatInt(int64(math.Round(f*255)), 16)
	if len(s) == 1 {
		s = "0" + s
	}
	return s
}
