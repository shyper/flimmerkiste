package logo

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// CaptionOptions describe the lower third shown over a trailer.
type CaptionOptions struct {
	Height int         // frame height (720 / 1080); the caption is sized relative to it
	Accent string      // "#rrggbb", optional
	Poster image.Image // optional
	Kicker string      // e.g. "Today at 10:00 PM" or "Coming to theaters · November 5"
	Title  string
	Sub    string  // e.g. "1985 · Comedy, Adventure"
	Scale  float64 // size relative to the trailer caption (0 = 1); the margin stays CaptionPad
	// MinWidth widens the panel to at least this many pixels, so the pages of
	// a changing card line up (see RenderCaptionPages).
	MinWidth int
}

// CaptionPad is the transparent margin around the panel (room for its shadow),
// as a fraction of the frame height. The overlay position has to subtract it.
const CaptionPad = 1.0 / 30

// RenderCaption draws the lower third as a transparent PNG: a dark glass panel
// with poster, a kicker tag in the channel colour, title and a detail line.
func RenderCaption(o CaptionOptions) ([]byte, error) {
	img, _, err := drawCaption(o)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderCaptionPages draws several lower thirds of the same size: each panel
// as wide as the widest one.
func RenderCaptionPages(pages []CaptionOptions) ([]*image.RGBA, error) {
	widest := 0
	out := make([]*image.RGBA, len(pages))
	widths := make([]int, len(pages))
	for i, o := range pages {
		img, pw, err := drawCaption(o)
		if err != nil {
			return nil, err
		}
		out[i], widths[i] = img, pw
		widest = max(widest, pw)
	}
	// only the narrower pages are drawn again, widened to the widest
	for i, o := range pages {
		if widths[i] == widest {
			continue
		}
		o.MinWidth = widest
		img, _, err := drawCaption(o)
		if err != nil {
			return nil, err
		}
		out[i] = img
	}
	return out, nil
}

// drawCaption draws the lower third and returns it with the width of its panel.
func drawCaption(o CaptionOptions) (*image.RGBA, int, error) {
	fh := o.Height
	if fh <= 0 {
		fh = 720
	}
	scale := o.Scale
	if scale <= 0 {
		scale = 1
	}
	s := float64(fh) / 720 * scale
	accent, ok := parseHex(o.Accent)
	if !ok {
		accent = hex("#e5a00d")
	}
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, 0, err
	}
	newFace := func(size float64) font.Face {
		f, _ := opentype.NewFace(bebas, &opentype.FaceOptions{Size: size * s, DPI: 72, Hinting: font.HintingNone})
		return f
	}
	kickerFace, titleFace, subFace := newFace(23), newFace(54), newFace(25)
	defer kickerFace.Close()
	defer titleFace.Close()
	defer subFace.Close()

	pad := int(float64(fh) * CaptionPad)
	ph := int(128 * s)
	posterW := 0
	if o.Poster != nil {
		posterW = ph * 2 / 3
	}
	inset := int(24 * s)
	maxText := int(float64(fh)*16/9*0.6*scale) - posterW - 2*inset

	title := fit(titleFace, o.Title, maxText)
	sub := fit(subFace, o.Sub, maxText)
	kicker := fit(kickerFace, strings.ToUpper(o.Kicker), maxText-int(24*s))
	textW := max(measure(titleFace, title), measure(subFace, sub), measure(kickerFace, kicker)+24*s)
	pw := max(posterW+2*inset+max(int(textW), int(300*s)), o.MinWidth)

	w, h := pw+2*pad, ph+2*pad
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	panel := image.Rect(pad, pad, pad+pw, pad+ph)
	radius := float32(14 * s)
	mask := roundRectMask(w, h, panel, radius)

	// soft drop shadow
	shadow := image.NewAlpha(img.Bounds())
	copy(shadow.Pix, mask.Pix)
	draw.DrawMask(img, img.Bounds(), image.NewUniform(color.NRGBA{0, 0, 0, 150}), image.Point{},
		blurAny(shadow, int(12*s)), image.Pt(0, -int(6*s)), draw.Over)

	// glass panel: dark, slightly tinted with the accent, more opaque behind the text
	base := mix(hex("#0d0e15"), accent, 0.10)
	fill := image.NewRGBA(img.Bounds())
	for x := panel.Min.X; x < panel.Max.X; x++ {
		t := float64(x-panel.Min.X) / float64(pw)
		c := color.NRGBA{base.R, base.G, base.B, uint8(238 - 40*t)}
		for y := panel.Min.Y; y < panel.Max.Y; y++ {
			fill.Set(x, y, c)
		}
	}
	// faint highlight along the top edge, on top of the panel colour (not
	// instead of it, or the panel would start a pixel lower than the poster)
	for x := panel.Min.X; x < panel.Max.X; x++ {
		for y := panel.Min.Y; y < panel.Min.Y+max(1, int(s)); y++ {
			c := fill.RGBAAt(x, y)
			lift := func(v uint8) uint8 { return v + uint8((int(c.A)-int(v))*34/255) } // premultiplied
			fill.SetRGBA(x, y, color.RGBA{lift(c.R), lift(c.G), lift(c.B), c.A})
		}
	}
	draw.DrawMask(img, img.Bounds(), fill, image.Point{}, mask, image.Point{}, draw.Over)

	// accent line along the whole bottom, fading out to the right: straight on
	// top, its ends follow the rounded corners of the panel
	line := max(3, int(4*s))
	strip := image.NewAlpha(img.Bounds())
	for y := panel.Max.Y - line; y < panel.Max.Y; y++ {
		for x := panel.Min.X; x < panel.Max.X; x++ {
			t := float64(x-panel.Min.X) / float64(pw)
			strip.SetAlpha(x, y, color.Alpha{uint8(float64(mask.AlphaAt(x, y).A) * (1 - t*t))})
		}
	}
	draw.DrawMask(img, img.Bounds(), image.NewUniform(accent), image.Point{}, strip, image.Point{}, draw.Over)

	if o.Poster != nil {
		// sits on the line; its left edge follows the panel
		frame := image.Rect(panel.Min.X, panel.Min.Y, panel.Min.X+posterW, panel.Max.Y-line)
		scaled := image.NewRGBA(img.Bounds())
		xdraw.CatmullRom.Scale(scaled, frame, o.Poster, coverRect(o.Poster.Bounds(), frame), xdraw.Src, nil)
		clip := image.NewAlpha(img.Bounds())
		draw.DrawMask(clip, frame, image.Opaque, image.Point{}, mask, frame.Min, draw.Src)
		draw.DrawMask(img, img.Bounds(), scaled, image.Point{}, clip, image.Point{}, draw.Over)
	}

	lineH := func(f font.Face) int { m := f.Metrics(); return m.Ascent.Ceil() + m.Descent.Ceil() }
	text := func(f font.Face, str string, c color.Color, x, y int) {
		d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: f, Dot: fixed.P(x, y+f.Metrics().Ascent.Ceil())}
		d.DrawString(str)
	}
	tx := panel.Min.X + posterW + inset
	top := panel.Min.Y
	if kicker != "" {
		// tag across the top edge (within the transparent margin): accent
		// background, dark or light text depending on the accent
		tagH := lineH(kickerFace) + int(8*s)
		kw := int(measure(kickerFace, kicker)) + int(20*s)
		tag := image.Rect(tx, panel.Min.Y-tagH/2, tx+kw, panel.Min.Y-tagH/2+tagH)
		tm := roundRectMask(w, h, tag, float32(5*s))
		ts := image.NewAlpha(img.Bounds())
		copy(ts.Pix, tm.Pix)
		draw.DrawMask(img, img.Bounds(), image.NewUniform(color.NRGBA{0, 0, 0, 120}), image.Point{},
			blurAny(ts, int(6*s)), image.Pt(0, -int(3*s)), draw.Over)
		draw.DrawMask(img, img.Bounds(), image.NewUniform(accent), image.Point{}, tm, image.Point{}, draw.Over)
		ink := color.Color(color.RGBA{14, 15, 22, 255})
		if luminance(accent) < 0.55 {
			ink = white
		}
		text(kickerFace, kicker, ink, tx+int(10*s), tag.Min.Y+int(5*s))
		top = tag.Max.Y
	}
	// title and detail line, vertically centred below the tag
	th := int(float64(lineH(titleFace)) * 0.82)
	sh := 0
	if sub != "" {
		sh = lineH(subFace) + int(2*s)
	}
	y := top + (panel.Max.Y-line-top-th-sh)/2
	text(titleFace, title, white, tx, y-int(float64(lineH(titleFace))*0.06))
	y += th
	if sub != "" {
		text(subFace, sub, color.RGBA{178, 182, 196, 255}, tx, y+int(2*s))
	}
	return img, pw, nil
}

// fit shortens text with an ellipsis until it fits maxWidth.
func fit(face font.Face, text string, maxWidth int) string {
	for measure(face, text) > float64(maxWidth) && len([]rune(text)) > 4 {
		r := []rune(text)
		text = strings.TrimSpace(string(r[:len(r)-2])) + "…"
	}
	return text
}

// coverRect returns the part of src that fills dst without distortion (centre crop).
func coverRect(src, dst image.Rectangle) image.Rectangle {
	sw, sh := src.Dx(), src.Dy()
	if sw*dst.Dy() > sh*dst.Dx() {
		cw := sh * dst.Dx() / max(1, dst.Dy())
		x := src.Min.X + (sw-cw)/2
		return image.Rect(x, src.Min.Y, x+cw, src.Max.Y)
	}
	ch := sw * dst.Dy() / max(1, dst.Dx())
	y := src.Min.Y + (sh-ch)/2
	return image.Rect(src.Min.X, y, src.Max.X, y+ch)
}

func luminance(c color.RGBA) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}
