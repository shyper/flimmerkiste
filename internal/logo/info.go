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
	"golang.org/x/image/vector"
)

// Weather and news slates: fillers in breaks next to the "coming to theaters"
// slates. All texts come translated and formatted from the caller.

// InfoHeader is the top of an info slate: channel logo, headline and a line
// below it (place and time of the data).
type InfoHeader struct {
	Width, Height int
	Logo          image.Image
	Accent        string
	Headline      string // "The weather"
	Line          string // "Hamburg · as of 8:30 AM"
	Credit        string // source, bottom left
	// Inline puts Line beside the headline instead of below it (more room below).
	Inline bool
	// Footer names the coming title when the slate is a page of the "up next"
	// slate (a box at the bottom right); nil on its own.
	Footer *InfoFooter
}

// InfoFooter is the box naming the coming title: "Stay tuned · At 8:15 PM",
// then the title.
type InfoFooter struct {
	Kicker, Title string
}

// WeatherSlot is one column: an hour or a day.
type WeatherSlot struct {
	Label string // "11 AM", "Saturday"
	Sub   string // "Oct 17" (days)
	Icon  string // Bright Sky icon name
	Temp  string // "13°" or high temperature of a day
	Low   string // low temperature of a day
	Rain  string // "Rain 20 %"
}

// WeatherOptions describe a weather slate. With Now.Icon set it shows the
// current weather big and Slots as the coming hours; without, Slots are days.
type WeatherOptions struct {
	InfoHeader
	Now       WeatherSlot // Temp and Icon; Label is the condition ("Partly cloudy")
	NowDetail string      // "Wind 12 km/h"
	Slots     []WeatherSlot
}

// NewsItem is one headline.
type NewsItem struct {
	Title string
	Meta  string // "tagesschau.de · 8:42 AM"
}

// NewsOptions describe a news slate.
type NewsOptions struct {
	InfoHeader
	Items []NewsItem
}

var (
	textGrey  = color.RGBA{170, 174, 188, 255}
	textLight = color.RGBA{225, 228, 236, 255}
)

// infoCanvas draws background, logo, headline and credit and returns the
// canvas, the scale and the y where the content starts.
func infoCanvas(o InfoHeader) (img *image.RGBA, s float64, top int, accent color.RGBA, err error) {
	w, h := o.Width, o.Height
	if w <= 0 || h <= 0 {
		w, h = 1280, 720
	}
	s = float64(h) / 720
	accent, ok := parseHex(o.Accent)
	if !ok {
		accent = hex("#e5a00d")
	}
	img = slateBackground(w, h, accent, 0.78, 0.3)
	margin := int(80 * s)
	y := int(64 * s)
	if lg := trimAlpha(o.Logo); lg != nil {
		lb := lg.Bounds()
		lh := int(56 * s)
		lw := lh * lb.Dx() / max(1, lb.Dy())
		if maxW := int(320 * s); lw > maxW {
			lw = maxW
			lh = lw * lb.Dy() / max(1, lb.Dx())
		}
		xdraw.CatmullRom.Scale(img, image.Rect(margin, y, margin+lw, y+lh), lg, lb, xdraw.Over, nil)
		y += lh + int(22*s)
	} else {
		y += int(24 * s)
	}
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, 0, 0, accent, err
	}
	maxW := w - 2*margin
	if o.Headline != "" {
		face := newFace(bebas, 54*s)
		head := strings.ToUpper(o.Headline)
		lineH := drawText(img, face, head, mix(accent, white, 0.15), margin, y, maxW)
		if o.Inline && o.Line != "" {
			// on the headline's baseline, after a gap
			lf := newFace(bebas, 32*s)
			x := margin + int(measure(face, head)) + int(22*s)
			drawText(img, lf, o.Line, color.RGBA{200, 203, 214, 255}, x, y+face.Metrics().Ascent.Ceil()-lf.Metrics().Ascent.Ceil(), maxW-(x-margin))
			lf.Close()
		}
		y += lineH
		face.Close()
	}
	if o.Line != "" && !(o.Inline && o.Headline != "") {
		face := newFace(bebas, 32*s)
		y += drawText(img, face, o.Line, color.RGBA{200, 203, 214, 255}, margin, y, maxW)
		face.Close()
	}
	if o.Credit != "" {
		face := newFace(bebas, 22*s)
		drawText(img, face, o.Credit, color.RGBA{150, 154, 168, 255}, margin, h-int(30*s)-face.Metrics().Ascent.Ceil(), maxW)
		face.Close()
	}
	if o.Footer != nil {
		drawFooter(img, bebas, *o.Footer, accent, s)
	}
	return img, s, y + int(36*s), accent, nil
}

// footerTop is where the footer box begins (at 720p), so content stays above it.
const footerTop = 600

// drawFooter draws the box naming the coming title at the bottom right.
func drawFooter(img *image.RGBA, bebas *opentype.Font, f InfoFooter, accent color.RGBA, s float64) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	px := func(v float64) int { return int(v * s) }
	kf, tf := newFace(bebas, 26*s), newFace(bebas, 40*s)
	defer kf.Close()
	defer tf.Close()
	kicker, title := strings.ToUpper(f.Kicker), strings.ToUpper(f.Title)
	maxText := w/2 - px(44)
	kicker, title = fit(kf, kicker, maxText), fit(tf, title, maxText)
	boxW := max(int(measure(kf, kicker)), int(measure(tf, title))) + px(44)
	right := w - px(80)
	box := image.Rect(right-boxW, px(footerTop), right, h-px(30))
	mask := roundRectMask(w, h, box, float32(14*s))
	draw.DrawMask(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 150}), image.Point{}, mask, image.Point{}, draw.Over)
	bar := image.Rect(box.Min.X, box.Max.Y-px(5), box.Max.X, box.Max.Y)
	draw.DrawMask(img, bar, image.NewUniform(accent), image.Point{}, mask, bar.Min, draw.Over)
	drawText(img, kf, kicker, mix(accent, white, 0.2), box.Min.X+px(22), box.Min.Y+px(10), maxText)
	drawText(img, tf, title, white, box.Min.X+px(22), box.Min.Y+px(38), maxText)
}

func newFace(f *opentype.Font, size float64) font.Face {
	face, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	return face
}

// drawCentered draws one line centred on cx with its top at y and returns its height.
func drawCentered(img *image.RGBA, face font.Face, text string, c color.Color, cx, y, maxW int) int {
	text = fit(face, text, maxW)
	return drawText(img, face, text, c, cx-int(measure(face, text)/2), y, maxW)
}

func infoPNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderWeather draws a weather slate as PNG.
func RenderWeather(o WeatherOptions) ([]byte, error) {
	img, s, top, _, err := infoCanvas(o.InfoHeader)
	if err != nil {
		return nil, err
	}
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, err
	}
	slab, err := loadFont("ZillaSlab-Bold.ttf")
	if err != nil {
		return nil, err
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	margin := int(80 * s)
	px := func(v float64) int { return int(v * s) }

	if o.Now.Icon != "" {
		// now: big icon and temperature on the left, the coming hours on the right
		icon := px(230)
		y := top + px(10)
		WeatherIcon(img, o.Now.Icon, image.Rect(margin, y, margin+icon, y+icon))
		tx := margin + icon + px(30)
		big := newFace(bebas, 190*s)
		th := drawText(img, big, o.Now.Temp, white, tx, y+px(4), px(330))
		big.Close()
		cond := newFace(bebas, 42*s)
		ty := y + int(float64(th)*0.82)
		ty += drawText(img, cond, o.Now.Label, textLight, tx, ty, px(360))
		cond.Close()
		if o.NowDetail != "" {
			det := newFace(slab, 24*s)
			drawText(img, det, o.NowDetail, textGrey, tx, ty+px(6), px(360))
			det.Close()
		}
		n := len(o.Slots)
		if n == 0 {
			return infoPNG(img)
		}
		left := px(760)
		colW := (w - margin - left) / n
		label, temp, rain := newFace(bebas, 32*s), newFace(bebas, 52*s), newFace(slab, 20*s)
		defer label.Close()
		defer temp.Close()
		defer rain.Close()
		for i, sl := range o.Slots {
			cx := left + colW*i + colW/2
			yy := top + px(20)
			yy += drawCentered(img, label, sl.Label, textGrey, cx, yy, colW) + px(10)
			is := px(92)
			WeatherIcon(img, sl.Icon, image.Rect(cx-is/2, yy, cx+is/2, yy+is))
			yy += is + px(10)
			yy += drawCentered(img, temp, sl.Temp, white, cx, yy, colW) + px(6)
			if sl.Rain != "" {
				drawCentered(img, rain, sl.Rain, textGrey, cx, yy, colW)
			}
		}
		return infoPNG(img)
	}

	// days: one panel per day across the width
	n := max(1, len(o.Slots))
	gap := px(24)
	colW := (w - 2*margin - gap*(n-1)) / n
	panelH := min(px(370), h-px(70)-top)
	day, date, temp, low, rain := newFace(bebas, 46*s), newFace(bebas, 28*s), newFace(bebas, 60*s), newFace(bebas, 42*s), newFace(slab, 21*s)
	defer day.Close()
	defer date.Close()
	defer temp.Close()
	defer low.Close()
	defer rain.Close()
	for i, sl := range o.Slots {
		x := margin + i*(colW+gap)
		panel := image.Rect(x, top, x+colW, top+panelH)
		mask := roundRectMask(w, h, panel, float32(18*s))
		draw.DrawMask(img, img.Bounds(), image.NewUniform(color.RGBA{16, 16, 16, 16}), image.Point{}, mask, image.Point{}, draw.Over)
		cx := x + colW/2
		yy := top + px(22)
		yy += drawCentered(img, day, sl.Label, white, cx, yy, colW-px(20))
		yy += drawCentered(img, date, sl.Sub, textGrey, cx, yy, colW-px(20)) + px(8)
		is := min(px(124), colW-px(40))
		WeatherIcon(img, sl.Icon, image.Rect(cx-is/2, yy, cx+is/2, yy+is))
		yy += is + px(12)
		// "20°" big, "/ 13°" smaller beside it, centred together
		hi, lo := sl.Temp, ""
		if sl.Low != "" {
			lo = " / " + sl.Low
		}
		wHi, wLo := int(measure(temp, hi)), int(measure(low, lo))
		sx := cx - (wHi+wLo)/2
		th := drawText(img, temp, hi, white, sx, yy, colW)
		base := yy + temp.Metrics().Ascent.Ceil()
		drawText(img, low, lo, textGrey, sx+wHi, base-low.Metrics().Ascent.Ceil(), colW)
		yy += th + px(6)
		if sl.Rain != "" {
			drawCentered(img, rain, sl.Rain, textGrey, cx, yy, colW-px(16))
		}
	}
	return infoPNG(img)
}

// RenderNews draws a news slate as PNG.
func RenderNews(o NewsOptions) ([]byte, error) {
	o.Inline = true
	img, s, top, accent, err := infoCanvas(o.InfoHeader)
	if err != nil {
		return nil, err
	}
	bebas, err := loadFont("BebasNeue-Regular.ttf")
	if err != nil {
		return nil, err
	}
	slab, err := loadFont("ZillaSlab-Bold.ttf")
	if err != nil {
		return nil, err
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	margin := int(80 * s)
	px := func(v float64) int { return int(v * s) }
	meta, title := newFace(bebas, 22*s), newFace(slab, 28*s)
	defer meta.Close()
	defer title.Close()
	// two columns, headlines alternating between them; each column moves down
	// on its own, so a long headline only pushes the one below it. The first
	// headline that does not fit ends the slate (fewer then).
	gap := px(48)
	colW := (w - 2*margin - gap) / 2
	maxW := colW - px(24)
	bottom := h - px(70)
	if o.Footer != nil {
		bottom = px(footerTop - 12)
	}
	lh := title.Metrics().Ascent.Ceil() + title.Metrics().Descent.Ceil()
	mh := meta.Metrics().Ascent.Ceil() + meta.Metrics().Descent.Ceil()
	ys := [2]int{top, top}
	for i, it := range o.Items {
		col := i % 2
		x, y := margin+col*(colW+gap), ys[col]
		lines := wrapText(title, it.Title, maxW, 4)
		need := mh + px(2) + len(lines)*lh
		if y+need > bottom {
			break
		}
		bar := image.Rect(x, y+px(3), x+px(5), y+need-px(3))
		draw.Draw(img, bar, image.NewUniform(accent), image.Point{}, draw.Over)
		yy := y + drawText(img, meta, it.Meta, mix(accent, white, 0.35), x+px(20), y, maxW) + px(2)
		for _, l := range lines {
			yy += drawText(img, title, l, white, x+px(20), yy, maxW)
		}
		ys[col] = yy + px(18)
	}
	return infoPNG(img)
}

// ---- weather icons ----

var (
	sunColor   = color.RGBA{255, 196, 48, 255}
	moonColor  = color.RGBA{232, 228, 206, 255}
	cloudLight = color.RGBA{228, 231, 239, 255}
	cloudDark  = color.RGBA{150, 156, 172, 255}
	rainColor  = color.RGBA{92, 172, 255, 255}
	boltColor  = color.RGBA{255, 214, 64, 255}
)

// shape fills a path given in units of the icon box (0..1) into an alpha mask.
type shape func(z *vector.Rasterizer, u func(x, y float64) (float32, float32))

func iconMask(size int, shapes ...shape) *image.Alpha {
	z := vector.NewRasterizer(size, size)
	u := func(x, y float64) (float32, float32) { return float32(x * float64(size)), float32(y * float64(size)) }
	for _, sh := range shapes {
		sh(z, u)
	}
	m := image.NewAlpha(image.Rect(0, 0, size, size))
	z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

func disc(cx, cy, r float64) shape {
	return func(z *vector.Rasterizer, u func(x, y float64) (float32, float32)) {
		const k = 0.5523 // cubic Bézier approximation of a quarter circle
		z.MoveTo(u(cx+r, cy))
		cubeTo(z, u, cx+r, cy+r*k, cx+r*k, cy+r, cx, cy+r)
		cubeTo(z, u, cx-r*k, cy+r, cx-r, cy+r*k, cx-r, cy)
		cubeTo(z, u, cx-r, cy-r*k, cx-r*k, cy-r, cx, cy-r)
		cubeTo(z, u, cx+r*k, cy-r, cx+r, cy-r*k, cx+r, cy)
		z.ClosePath()
	}
}

func cubeTo(z *vector.Rasterizer, u func(x, y float64) (float32, float32), x1, y1, x2, y2, x3, y3 float64) {
	ax, ay := u(x1, y1)
	bx, by := u(x2, y2)
	cx, cy := u(x3, y3)
	z.CubeTo(ax, ay, bx, by, cx, cy)
}

// poly is a closed polygon of x, y pairs.
func poly(pts ...float64) shape {
	return func(z *vector.Rasterizer, u func(x, y float64) (float32, float32)) {
		z.MoveTo(u(pts[0], pts[1]))
		for i := 2; i+1 < len(pts); i += 2 {
			z.LineTo(u(pts[i], pts[i+1]))
		}
		z.ClosePath()
	}
}

// line is a thick straight stroke with square ends.
func line(x1, y1, x2, y2, width float64) shape {
	dx, dy := x2-x1, y2-y1
	l := math.Hypot(dx, dy)
	nx, ny := -dy/l*width/2, dx/l*width/2
	return poly(x1+nx, y1+ny, x2+nx, y2+ny, x2-nx, y2-ny, x1-nx, y1-ny)
}

func cloudShapes(dx, dy, sc float64) []shape {
	p := func(x, y float64) (float64, float64) { return dx + x*sc, dy + y*sc }
	c := func(x, y, r float64) shape { px, py := p(x, y); return disc(px, py, r*sc) }
	x1, y1 := p(0.2, 0.55)
	x2, y2 := p(0.82, 0.75)
	return []shape{c(0.34, 0.58, 0.16), c(0.53, 0.46, 0.21), c(0.72, 0.6, 0.14), poly(x1, y1, x2, y1, x2, y2, x1, y2), c(0.2, 0.6, 0.15), c(0.82, 0.62, 0.13)}
}

func sunShapes(cx, cy, r float64) []shape {
	out := []shape{disc(cx, cy, r)}
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		out = append(out, line(cx+math.Cos(a)*r*1.35, cy+math.Sin(a)*r*1.35, cx+math.Cos(a)*r*1.85, cy+math.Sin(a)*r*1.85, r*0.28))
	}
	return out
}

// moonMask is a crescent: a disc with an offset disc cut away.
func moonMask(size int, cx, cy, r float64) *image.Alpha {
	m := iconMask(size, disc(cx, cy, r))
	cut := iconMask(size, disc(cx+r*0.55, cy-r*0.35, r*0.85))
	for i := range m.Pix {
		m.Pix[i] = uint8(int(m.Pix[i]) * (255 - int(cut.Pix[i])) / 255)
	}
	return m
}

// WeatherIcon draws the icon of a Bright Sky condition into r.
func WeatherIcon(img *image.RGBA, name string, r image.Rectangle) {
	size := min(r.Dx(), r.Dy())
	if size <= 0 {
		return
	}
	at := r.Min.Add(image.Pt((r.Dx()-size)/2, (r.Dy()-size)/2))
	r = image.Rectangle{Min: at, Max: at.Add(image.Pt(size, size))}
	paint := func(c color.RGBA, m *image.Alpha) {
		draw.DrawMask(img, r, image.NewUniform(c), image.Point{}, m, image.Point{}, draw.Over)
	}
	// a soft shadow under clouds sets them off from the sun and the background
	cloud := func(c color.RGBA, dx, dy, sc float64) {
		m := iconMask(size, cloudShapes(dx, dy, sc)...)
		sh := image.NewAlpha(m.Bounds())
		copy(sh.Pix, m.Pix)
		draw.DrawMask(img, r.Add(image.Pt(0, size/40)), image.NewUniform(color.RGBA{0, 0, 0, 110}), image.Point{}, blurAny(sh, max(1, size/30)), image.Point{}, draw.Over)
		paint(c, m)
	}
	drops := func(c color.RGBA, flakes bool) {
		for i, x := range []float64{0.34, 0.52, 0.7} {
			y := 0.8 + float64(i%2)*0.06
			if flakes {
				paint(c, iconMask(size, disc(x, y+0.04, 0.045)))
			} else {
				paint(c, iconMask(size, line(x, y, x-0.05, y+0.13, 0.05)))
			}
		}
	}
	switch name {
	case "clear-day":
		paint(sunColor, iconMask(size, sunShapes(0.5, 0.5, 0.2)...))
	case "clear-night":
		paint(moonColor, moonMask(size, 0.47, 0.53, 0.3))
	case "partly-cloudy-day":
		paint(sunColor, iconMask(size, sunShapes(0.36, 0.34, 0.15)...))
		cloud(cloudLight, 0.06, 0.12, 0.9)
	case "partly-cloudy-night":
		paint(moonColor, moonMask(size, 0.36, 0.34, 0.2))
		cloud(cloudLight, 0.06, 0.12, 0.9)
	case "rain", "sleet", "snow", "hail":
		cloud(cloudDark, 0.02, -0.12, 0.95)
		switch name {
		case "rain":
			drops(rainColor, false)
		case "snow":
			drops(white, true)
		case "hail":
			drops(cloudLight, true)
		case "sleet":
			paint(rainColor, iconMask(size, line(0.36, 0.8, 0.31, 0.93, 0.05), line(0.7, 0.8, 0.65, 0.93, 0.05)))
			paint(white, iconMask(size, disc(0.52, 0.9, 0.045)))
		}
	case "thunderstorm":
		cloud(cloudDark, 0.02, -0.12, 0.95)
		paint(boltColor, iconMask(size, poly(0.55, 0.62, 0.4, 0.82, 0.5, 0.82, 0.44, 0.98, 0.64, 0.74, 0.53, 0.74, 0.6, 0.62)))
	case "fog":
		cloud(cloudDark, 0.02, -0.14, 0.9)
		for i, y := range []float64{0.7, 0.8, 0.9} {
			x := 0.18 + float64(i%2)*0.08
			paint(cloudLight, iconMask(size, line(x, y, x+0.58, y, 0.05)))
		}
	case "wind":
		for i, y := range []float64{0.36, 0.5, 0.64} {
			l := []float64{0.5, 0.66, 0.42}[i]
			x := 0.16 + float64(i%2)*0.1
			paint(cloudLight, iconMask(size, line(x, y, x+l, y, 0.055)))
		}
	default: // "cloudy" and unknown
		cloud(cloudDark, 0.12, -0.08, 0.8)
		cloud(cloudLight, -0.02, 0.06, 0.9)
	}
}
