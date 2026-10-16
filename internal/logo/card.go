package logo

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// CardOptions describe an "up next" slate shown between programs.
type CardOptions struct {
	Width, Height int
	Logo          image.Image // channel logo (transparent PNG), optional
	Poster        image.Image // poster of the next title, optional
	Accent        string      // "#rrggbb", optional
	Headline      string      // e.g. "Stay tuned"
	When          string      // e.g. "At 8:15 PM"
	Title         string
	Subtitle      string
	// "Coming to theaters" slates: a backdrop instead of the plain background,
	// a short summary under the subtitle and a source credit at the bottom right.
	Backdrop image.Image
	Summary  string
	Credit   string
}

// RenderCard draws the slate as PNG.
func RenderCard(o CardOptions) ([]byte, error) {
	w, h := o.Width, o.Height
	if w <= 0 || h <= 0 {
		w, h = 1280, 720
	}
	s := float64(h) / 720 // scale factor relative to the 720p layout
	accent, ok := parseHex(o.Accent)
	if !ok {
		accent = hex("#e5a00d")
	}
	img := slateBackground(w, h, accent, 0.22, 0.55)
	if o.Backdrop != nil {
		backdrop(img, o.Backdrop)
	}
	o.Logo = trimAlpha(o.Logo)

	// the same margin left of the poster and right of the text (and the clock)
	margin := int(80 * s)
	textX := margin
	if o.Poster != nil {
		ph := int(float64(h) * 0.68)
		pw := ph * 2 / 3
		px, py := margin, (h-ph)/2
		frame := image.Rect(px, py, px+pw, py+ph)
		mask := roundRectMask(w, h, frame, float32(16*s))
		shadow := image.NewAlpha(img.Bounds())
		copy(shadow.Pix, mask.Pix)
		draw.DrawMask(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 160}), image.Point{}, blurAny(shadow, int(18*s)), image.Pt(0, -int(14*s)), draw.Over)
		scaled := image.NewRGBA(img.Bounds())
		xdraw.CatmullRom.Scale(scaled, frame, o.Poster, o.Poster.Bounds(), xdraw.Src, nil)
		draw.DrawMask(img, img.Bounds(), scaled, image.Point{}, mask, image.Point{}, draw.Over)
		textX = px + pw + int(70*s)
	}
	maxW := w - textX - margin

	// Logo and text go onto their own layer first: then the block is centred
	// vertically on the poster (the frame) by its visible ink, however many lines it has.
	layer := image.NewRGBA(img.Bounds())
	y := int(150 * s)
	if o.Logo != nil {
		lb := o.Logo.Bounds()
		lw := int(float64(maxW) * 0.42)
		lh := lw * lb.Dy() / max(1, lb.Dx())
		if maxLH := int(84 * s); lh > maxLH { // the visible logo, without its margin
			lh = maxLH
			lw = lh * lb.Dx() / max(1, lb.Dy())
		}
		dst := image.Rect(textX, y-int(70*s), textX+lw, y-int(70*s)+lh)
		xdraw.CatmullRom.Scale(layer, dst, o.Logo, lb, xdraw.Over, nil)
		y = dst.Max.Y + int(40*s)
	}

	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, err
	}
	drawLine := func(text string, f *opentype.Font, size float64, c color.RGBA, maxWidth int) int {
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return 0
		}
		defer face.Close()
		for measure(face, text) > float64(maxWidth) && len([]rune(text)) > 4 {
			r := []rune(text)
			text = strings.TrimSpace(string(r[:len(r)-2])) + "…"
		}
		m := face.Metrics()
		asc := m.Ascent.Ceil()
		d := &font.Drawer{Dst: layer, Src: image.NewUniform(c), Face: face, Dot: fixed.P(textX, y+asc)}
		d.DrawString(text)
		return asc + m.Descent.Ceil()
	}

	if o.Headline != "" {
		y += drawLine(strings.ToUpper(o.Headline), bebas, 44*s, mix(accent, white, 0.15), maxW) + int(4*s)
	}
	if o.When != "" {
		y += drawLine(o.When, bebas, 34*s, color.RGBA{200, 203, 214, 255}, maxW) + int(18*s)
	}
	// Title: as large as possible, up to two lines.
	if o.Title != "" {
		words := strings.Fields(o.Title)
		size := 110 * s
		if o.Summary != "" {
			size = 80 * s // room for the summary below
		}
		var lines []string
		for ; size > 40*s; size -= 4 * s {
			face, _ := opentype.NewFace(bebas, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
			width := func(t string) float64 { return measure(face, t) }
			lines = []string{o.Title}
			if width(o.Title) > float64(maxW) && len(words) > 1 {
				lines = splitBalanced(words, 2, width)
			}
			fits := true
			for _, l := range lines {
				if width(l) > float64(maxW) {
					fits = false
				}
			}
			face.Close()
			if fits {
				break
			}
		}
		for _, l := range lines {
			y += int(float64(drawLine(l, bebas, size, white, maxW)) * 0.86)
		}
		y += int(14 * s)
	}
	if o.Subtitle != "" {
		y += drawLine(o.Subtitle, bebas, 38*s, color.RGBA{170, 174, 188, 255}, maxW)
	}
	if o.Summary != "" {
		slab, err := loadFont("ZillaSlab-Bold.ttf")
		if err != nil {
			return nil, err
		}
		face, err := opentype.NewFace(slab, &opentype.FaceOptions{Size: 22 * s, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return nil, err
		}
		y += int(16 * s)
		for _, l := range wrapText(face, o.Summary, maxW, 3) {
			y += drawText(layer, face, l, color.RGBA{190, 194, 206, 255}, textX, y, maxW) + int(4*s)
		}
		face.Close()
	}
	if ink := visibleBounds(layer); !ink.Empty() {
		dy := h/2 - (ink.Min.Y+ink.Max.Y)/2
		dy = max(min(dy, h-margin-ink.Max.Y), margin-ink.Min.Y) // stays inside the frame's margins
		draw.Draw(img, layer.Bounds().Add(image.Pt(0, dy)), layer, image.Point{}, draw.Over)
	}
	if o.Credit != "" {
		if face, err := opentype.NewFace(bebas, &opentype.FaceOptions{Size: 22 * s, DPI: 72, Hinting: font.HintingNone}); err == nil {
			d := &font.Drawer{Dst: img, Src: image.NewUniform(color.RGBA{150, 154, 168, 255}), Face: face,
				Dot: fixed.P(margin, h-int(30*s))}
			d.DrawString(o.Credit)
			face.Close()
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// slateBackground is the backdrop of all slates: a dark vertical gradient plus
// a soft accent glow centred at (cx, cy), given as fractions of the size.
func slateBackground(w, h int, accent color.RGBA, cx, cy float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	top, bottom := hex("#101119"), mix(hex("#0a0b10"), accent, 0.12)
	for y := 0; y < h; y++ {
		c := mix(top, bottom, float64(y)/float64(h))
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	glowCX, glowCY, glowR := float64(w)*cx, float64(h)*cy, float64(h)*0.75
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := math.Hypot(float64(x)-glowCX, float64(y)-glowCY) / glowR
			if d < 1 {
				a := (1 - d) * (1 - d) * 0.35
				img.SetRGBA(x, y, mix(img.RGBAAt(x, y), accent, a))
			}
		}
	}
	return img
}

func roundRectMask(w, h int, r image.Rectangle, radius float32) *image.Alpha {
	z := vector.NewRasterizer(w, h)
	x0, y0, x1, y1 := float32(r.Min.X), float32(r.Min.Y), float32(r.Max.X), float32(r.Max.Y)
	z.MoveTo(x0+radius, y0)
	z.LineTo(x1-radius, y0)
	z.QuadTo(x1, y0, x1, y0+radius)
	z.LineTo(x1, y1-radius)
	z.QuadTo(x1, y1, x1-radius, y1)
	z.LineTo(x0+radius, y1)
	z.QuadTo(x0, y1, x0, y1-radius)
	z.LineTo(x0, y0+radius)
	z.QuadTo(x0, y0, x0+radius, y0)
	z.ClosePath()
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

// blurAny is a box blur for masks of any size (the logo code uses the fixed logo canvas).
func blurAny(a *image.Alpha, r int) *image.Alpha {
	w, h := a.Rect.Dx(), a.Rect.Dy()
	out := image.NewAlpha(a.Rect)
	copy(out.Pix, a.Pix)
	tmp := make([]int, w*h)
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < h; y++ {
			sum := 0
			for x := -r; x <= r; x++ {
				sum += int(out.Pix[y*out.Stride+clamp(x, 0, w-1)])
			}
			for x := 0; x < w; x++ {
				tmp[y*w+x] = sum / (2*r + 1)
				sum += int(out.Pix[y*out.Stride+clamp(x+r+1, 0, w-1)]) - int(out.Pix[y*out.Stride+clamp(x-r, 0, w-1)])
			}
		}
		for x := 0; x < w; x++ {
			sum := 0
			for y := -r; y <= r; y++ {
				sum += tmp[clamp(y, 0, h-1)*w+x]
			}
			for y := 0; y < h; y++ {
				out.Pix[y*out.Stride+x] = uint8(sum / (2*r + 1))
				sum += tmp[clamp(y+r+1, 0, h-1)*w+x] - tmp[clamp(y-r, 0, h-1)*w+x]
			}
		}
	}
	return out
}

// backdrop covers img with a picture, darkened towards the left and the
// bottom, so text in front of it stays readable.
func backdrop(img *image.RGBA, pic image.Image) {
	r := img.Bounds()
	xdraw.CatmullRom.Scale(img, r, pic, coverRect(pic.Bounds(), r), xdraw.Src, nil)
	w, h := r.Dx(), r.Dy()
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(w)
			// darkest on the left where the text is, a little at the bottom
			a := 0.55 + 0.35*(1-fx) + 0.25*fy*fy
			if a > 0.94 {
				a = 0.94
			}
			i := img.PixOffset(x, y)
			for c := 0; c < 3; c++ {
				img.Pix[i+c] = uint8(float64(img.Pix[i+c])*(1-a) + 10*a)
			}
		}
	}
}
