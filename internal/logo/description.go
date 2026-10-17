package logo

import (
	"image"
	"image/color"
	"image/draw"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// RenderDescription draws the page of the "up next" slate that tells what the
// coming title is about: the poster on the left as on the slate, on the right
// the start time (When), the title, the line with year and genres and the
// summary (up to 8 lines).
func RenderDescription(o CardOptions) ([]byte, error) {
	w, h := o.Width, o.Height
	if w <= 0 || h <= 0 {
		w, h = 1280, 720
	}
	s := float64(h) / 720
	px := func(v float64) int { return int(v * s) }
	accent, ok := parseHex(o.Accent)
	if !ok {
		accent = hex("#e5a00d")
	}
	img := slateBackground(w, h, accent, 0.22, 0.55)
	margin := px(80)
	textX := margin
	if o.Poster != nil {
		// placed exactly like on the slate, so the poster stays put when the pages change
		ph := int(float64(h) * 0.68)
		pw := ph * 2 / 3
		px0, py := margin, (h-ph)/2
		frame := image.Rect(px0, py, px0+pw, py+ph)
		mask := roundRectMask(w, h, frame, float32(16*s))
		shadow := image.NewAlpha(img.Bounds())
		copy(shadow.Pix, mask.Pix)
		draw.DrawMask(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 160}), image.Point{}, blurAny(shadow, px(18)), image.Pt(0, -px(14)), draw.Over)
		scaled := image.NewRGBA(img.Bounds())
		xdraw.CatmullRom.Scale(scaled, frame, o.Poster, o.Poster.Bounds(), xdraw.Src, nil)
		draw.DrawMask(img, img.Bounds(), scaled, image.Point{}, mask, image.Point{}, draw.Over)
		textX = px0 + pw + px(70)
	}
	maxW := w - textX - margin
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, err
	}
	slab, err := loadFont("ZillaSlab-Bold.ttf")
	if err != nil {
		return nil, err
	}
	// text on its own layer, centred vertically by its ink like the slate
	layer := image.NewRGBA(img.Bounds())
	y := 0
	if o.When != "" {
		f := newFace(bebas, 34*s)
		y += drawText(layer, f, strings.ToUpper(o.When), mix(accent, white, 0.15), textX, y, maxW) + px(8)
		f.Close()
	}
	y += drawTitle(layer, bebas, o.Title, textX, y, maxW, px(150), 84*s, 50*s) + px(10)
	if o.Subtitle != "" {
		f := newFace(bebas, 34*s)
		y += drawText(layer, f, o.Subtitle, color.RGBA{170, 174, 188, 255}, textX, y, maxW) + px(22)
		f.Close()
	}
	f := newFace(slab, 27*s)
	for _, l := range wrapText(f, o.Summary, maxW, 8) {
		y += drawText(layer, f, l, color.RGBA{210, 213, 222, 255}, textX, y, maxW) + px(6)
	}
	f.Close()
	if ink := visibleBounds(layer); !ink.Empty() {
		dy := h/2 - (ink.Min.Y+ink.Max.Y)/2
		draw.Draw(img, layer.Bounds().Add(image.Pt(0, dy)), layer, image.Point{}, draw.Over)
	}
	return infoPNG(img)
}
