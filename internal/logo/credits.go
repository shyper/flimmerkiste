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
)

// CreditsOptions describe the screen around the shrunk end credits: what
// follows on this channel at the top right, highlights of other channels below.
type CreditsOptions struct {
	Width, Height int
	Video         image.Rectangle // the box the credits play in; kept free
	Picture       image.Rectangle // the part of Video their picture fills (gets the shadow; empty = all of it)
	Logo          image.Image     // channel logo, optional
	Accent        string          // "#rrggbb", optional
	Headline      string          // e.g. "Stay tuned"
	When          string          // e.g. "At 8:15 PM"
	Title         string
	Subtitle      string
	Seen          string // under the subtitle, e.g. "Last watched 2 years ago"
	Kicker        string // above the highlights, e.g. "Coming up on other channels"
	Highlights    []Highlight
}

// Highlight is a program on another channel, shown one at a time.
type Highlight struct {
	Logo     image.Image // logo of that channel, optional
	When     string      // e.g. "8:15 PM · Comedies"
	Title    string
	Subtitle string // e.g. "1985 · Adventure, Comedy"
	Seen     string // e.g. "Last watched 2 years ago"
	Summary  string
	Poster   image.Image
}

// RenderCredits draws one full frame per highlight (one without highlights);
// they only differ below the credits.
func RenderCredits(o CreditsOptions) ([]*image.RGBA, error) {
	w, h := o.Width, o.Height
	if w <= 0 || h <= 0 {
		w, h = 1280, 720
	}
	s := float64(h) / 720
	accent, ok := parseHex(o.Accent)
	if !ok {
		accent = hex("#e5a00d")
	}
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, err
	}
	slab, err := loadFont("ZillaSlab-Bold.ttf")
	if err != nil {
		return nil, err
	}
	newFace := func(f *opentype.Font, size float64) font.Face {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size * s, DPI: 72, Hinting: font.HintingNone})
		return face
	}

	base := slateBackground(w, h, accent, 0.75, 0.25)
	// logos without their transparent margin, so they line up with the text and the picture
	o.Logo = trimAlpha(o.Logo)
	for i := range o.Highlights {
		o.Highlights[i].Logo = trimAlpha(o.Highlights[i].Logo)
	}
	video := o.Video
	if video.Empty() {
		video = image.Rect(w*3/100, w*3/100, w*49/100, w*3/100+w*46/100*9/16)
	}
	// soft shadow under the credits' picture
	pic := o.Picture
	if pic.Empty() {
		pic = video
	}
	vm := roundRectMask(w, h, pic, float32(6*s))
	draw.DrawMask(base, base.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 170}), image.Point{},
		blurAny(vm, int(16*s)), image.Pt(0, -int(10*s)), draw.Over)

	// top right: what follows on this channel
	margin := video.Min.X
	x := video.Max.X + int(44*s)
	maxW := w - margin - x
	y := video.Min.Y
	if o.Logo != nil {
		lb := o.Logo.Bounds()
		lh := int(56 * s) // the visible logo, without its margin
		lw := lh * lb.Dx() / max(1, lb.Dy())
		if lw > maxW*6/10 {
			lw = maxW * 6 / 10
			lh = lw * lb.Dy() / max(1, lb.Dx())
		}
		xdraw.CatmullRom.Scale(base, image.Rect(x, y, x+lw, y+lh), o.Logo, lb, xdraw.Over, nil)
		y += lh + int(22*s)
	}
	if o.Headline != "" {
		y += drawText(base, newFace(bebas, 38), strings.ToUpper(o.Headline), mix(accent, white, 0.15), x, y, maxW)
	}
	if o.When != "" {
		y += drawText(base, newFace(bebas, 30), o.When, color.RGBA{200, 203, 214, 255}, x, y, maxW) + int(10*s)
	}
	if o.Title != "" {
		// as large as fits in two lines and above the subtitle
		bottom := video.Max.Y
		if o.Subtitle != "" {
			bottom -= int(34 * s)
		}
		if o.Seen != "" {
			bottom -= int(32 * s)
		}
		y += drawTitle(base, bebas, o.Title, x, y, maxW, bottom-y, 84*s, 34*s)
	}
	if o.Subtitle != "" {
		y += drawText(base, newFace(bebas, 28), o.Subtitle, color.RGBA{170, 174, 188, 255}, x, y+int(4*s), maxW) + int(4*s)
	}
	if o.Seen != "" {
		drawText(base, newFace(bebas, 26), o.Seen, seenColor(accent), x, y+int(6*s), maxW)
	}

	if len(o.Highlights) == 0 {
		return []*image.RGBA{base}, nil
	}
	// below: one highlight per frame
	top := video.Max.Y + int(34*s)
	bottom := h - margin
	width := w - 2*margin
	kickerFace := newFace(bebas, 28)
	kh := 0
	if o.Kicker != "" {
		kh = drawText(base, kickerFace, strings.ToUpper(o.Kicker), mix(accent, white, 0.15), margin, top, width)
		// accent line under the kicker, fading out to the right
		ly, lt := top+kh+int(4*s), max(2, int(3*s))
		for lx := margin; lx < margin+width; lx++ {
			t := float64(lx-margin) / float64(width)
			for yy := ly; yy < ly+lt; yy++ {
				base.SetRGBA(lx, yy, mix(base.RGBAAt(lx, yy), accent, (1-t)*(1-t)))
			}
		}
		kh += int(18 * s)
	}
	frames := make([]*image.RGBA, len(o.Highlights))
	for i, hl := range o.Highlights {
		img := image.NewRGBA(base.Bounds())
		copy(img.Pix, base.Pix)
		if n := len(o.Highlights); n > 1 {
			// page dots at the right end of the kicker line
			r := int(5 * s)
			cy := top + int(11*s)
			for j := 0; j < n; j++ {
				cx := w - margin - (n-1-j)*int(20*s) - r
				c := color.RGBA{90, 94, 108, 255}
				if j == i {
					c = accent
				}
				dot := roundRectMask(w, h, image.Rect(cx-r, cy-r, cx+r, cy+r), float32(r))
				draw.DrawMask(img, img.Bounds(), image.NewUniform(c), image.Point{}, dot, image.Point{}, draw.Over)
			}
		}
		drawHighlight(img, hl, image.Rect(margin, top+kh, margin+width, bottom), s, accent, bebas, slab)
		frames[i] = img
	}
	return frames, nil
}

// drawHighlight draws poster, channel, time, title, details and summary into r.
func drawHighlight(img *image.RGBA, hl Highlight, r image.Rectangle, s float64, accent color.RGBA, bebas, slab *opentype.Font) {
	newFace := func(f *opentype.Font, size float64) font.Face {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size * s, DPI: 72, Hinting: font.HintingNone})
		return face
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	x := r.Min.X
	if hl.Poster != nil {
		ph := r.Dy()
		frame := image.Rect(r.Min.X, r.Min.Y, r.Min.X+ph*2/3, r.Max.Y)
		mask := roundRectMask(w, h, frame, float32(10*s))
		shadow := image.NewAlpha(img.Bounds())
		copy(shadow.Pix, mask.Pix)
		draw.DrawMask(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, blurAny(shadow, int(12*s)), image.Pt(0, -int(8*s)), draw.Over)
		scaled := image.NewRGBA(img.Bounds())
		xdraw.CatmullRom.Scale(scaled, frame, hl.Poster, coverRect(hl.Poster.Bounds(), frame), xdraw.Src, nil)
		draw.DrawMask(img, img.Bounds(), scaled, image.Point{}, mask, image.Point{}, draw.Over)
		x = frame.Max.X + int(30*s)
	}
	maxW := r.Max.X - x
	y := r.Min.Y + int(4*s)
	lineH := int(36 * s)
	tx := x
	if hl.Logo != nil {
		lb := hl.Logo.Bounds()
		lh := lineH
		lw := lh * lb.Dx() / max(1, lb.Dy())
		if lw > maxW/3 {
			lw = maxW / 3
			lh = lw * lb.Dy() / max(1, lb.Dx())
		}
		xdraw.CatmullRom.Scale(img, image.Rect(x, y+(lineH-lh)/2, x+lw, y+(lineH-lh)/2+lh), hl.Logo, lb, xdraw.Over, nil)
		tx = x + lw + int(16*s)
	}
	if hl.When != "" {
		face := newFace(bebas, 30)
		m := face.Metrics()
		drawText(img, face, hl.When, color.RGBA{200, 203, 214, 255}, tx, y+(lineH-m.Ascent.Ceil()-m.Descent.Ceil())/2+int(2*s), r.Max.X-tx)
	}
	y += lineH + int(10*s)
	y += drawTitle(img, bebas, hl.Title, x, y, maxW, int(64*s), 62*s, 34*s)
	// the "last watched" line sits close under the subtitle, leaving room for the summary
	gap := int(12 * s)
	if hl.Seen != "" {
		gap = int(4 * s)
	}
	if hl.Subtitle != "" {
		y += drawText(img, newFace(bebas, 28), hl.Subtitle, color.RGBA{170, 174, 188, 255}, x, y+int(2*s), maxW) + gap
	}
	if hl.Seen != "" {
		y += drawText(img, newFace(bebas, 24), hl.Seen, seenColor(accent), x, y, maxW) + int(8*s)
	}
	if hl.Summary != "" {
		face := newFace(slab, 20)
		m := face.Metrics()
		lh := m.Ascent.Ceil() + m.Descent.Ceil() + int(4*s)
		lines := wrapText(face, hl.Summary, maxW, max(0, (r.Max.Y-y)/lh))
		for _, l := range lines {
			drawText(img, face, l, color.RGBA{150, 154, 168, 255}, x, y, maxW)
			y += lh
		}
	}
}

// drawText draws one line with its top at y, shortened to maxW, and returns its height.
func drawText(img *image.RGBA, face font.Face, text string, c color.Color, x, y, maxW int) int {
	text = fit(face, text, maxW)
	m := face.Metrics()
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y+m.Ascent.Ceil())}
	d.DrawString(text)
	return m.Ascent.Ceil() + m.Descent.Ceil()
}

// drawTitle draws a title as large as possible (maxSize down to minSize) in up
// to two lines within maxW × maxH and returns the height used.
func drawTitle(img *image.RGBA, f *opentype.Font, title string, x, y, maxW, maxH int, maxSize, minSize float64) int {
	words := strings.Fields(title)
	if len(words) == 0 {
		return 0
	}
	size := maxSize
	var lines []string
	for ; size > minSize; size -= 2 {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		width := func(t string) float64 { return measure(face, t) }
		lines = []string{title}
		if width(title) > float64(maxW) && len(words) > 1 {
			lines = splitBalanced(words, 2, width)
		}
		fits := float64(len(lines))*size*0.92 <= float64(maxH)
		for _, l := range lines {
			fits = fits && width(l) <= float64(maxW)
		}
		face.Close()
		if fits {
			break
		}
	}
	if float64(len(lines))*size*0.92 > float64(maxH) {
		lines = []string{title} // still too tall: one line, shortened
	}
	used := 0
	for _, l := range lines {
		face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
		used += int(float64(drawText(img, face, l, white, x, y+used, maxW)) * 0.86)
	}
	return used
}

// wrapText breaks text into at most maxLines lines of maxW; the last one ends
// with an ellipsis when text is left over.
func wrapText(face font.Face, text string, maxW, maxLines int) []string {
	var lines []string
	cur := ""
	words := strings.Fields(text)
	for i, word := range words {
		next := strings.TrimSpace(cur + " " + word)
		if measure(face, next) <= float64(maxW) || cur == "" {
			cur = next
			continue
		}
		lines = append(lines, cur)
		cur = word
		if len(lines) == maxLines {
			last := lines[maxLines-1]
			if i < len(words) {
				last = fit(face, last+" …", maxW)
				if !strings.HasSuffix(last, "…") {
					last += " …"
				}
			}
			lines[maxLines-1] = last
			return lines
		}
	}
	if cur != "" && len(lines) < maxLines {
		lines = append(lines, cur)
	}
	return lines
}

// Blend mixes two frames of the same size: t = 0 gives a, t = 1 gives b.
func Blend(a, b *image.RGBA, t float64) *image.RGBA {
	out := image.NewRGBA(a.Bounds())
	if len(b.Pix) != len(a.Pix) {
		copy(out.Pix, a.Pix) // frames of different sizes cannot be mixed
		return out
	}
	k := int(math.Max(0, math.Min(1, t))*256 + 0.5)
	for i := range out.Pix {
		out.Pix[i] = uint8((int(a.Pix[i])*(256-k) + int(b.Pix[i])*k) >> 8)
	}
	return out
}

// RenderClock draws a time ("20:15") as a transparent PNG for a frame of the
// given height: right-aligned on a canvas wide enough for any time, so every
// minute has the same size, with a soft shadow in a margin of pad.
func RenderClock(text string, height, pad int) ([]byte, error) {
	s := float64(height) / 720
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, err
	}
	face, _ := opentype.NewFace(bebas, &opentype.FaceOptions{Size: 46 * s, DPI: 72, Hinting: font.HintingNone})
	defer face.Close()
	m := face.Metrics()
	// The canvas fits the widest time of the same format ("00:00", "00:00 PM"),
	// so it keeps its size from minute to minute.
	tw := int(max(measure(face, clockTemplate(text)), measure(face, text))) + 2
	w, h := tw+2*pad, m.Ascent.Ceil()+m.Descent.Ceil()+2*pad
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	x := w - pad - int(measure(face, text))
	shadow := image.NewAlpha(img.Bounds())
	(&font.Drawer{Dst: shadow, Src: image.Opaque, Face: face, Dot: fixed.P(x, pad+m.Ascent.Ceil())}).DrawString(text)
	draw.DrawMask(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 140}), image.Point{},
		blurAny(shadow, max(1, int(3*s))), image.Pt(0, -int(2*s)), draw.Over)
	(&font.Drawer{Dst: img, Src: image.NewUniform(white), Face: face, Dot: fixed.P(x, pad+m.Ascent.Ceil())}).DrawString(text)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// clockTemplate is text with every digit turned into a 0.
func clockTemplate(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '0'
		}
		return r
	}, text)
}
