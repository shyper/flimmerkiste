package logo

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
)

// Twemoji 15.1 (72×72 PNGs, CC-BY 4.0, see emoji/LICENSE-GRAPHICS), stored uncompressed in a zip.
//
//go:embed emoji/twemoji-72.zip
var twemojiZip []byte

var (
	emojiOnce  sync.Once
	emojiFiles map[string]*zip.File
)

func emojiIndex() map[string]*zip.File {
	emojiOnce.Do(func() {
		emojiFiles = map[string]*zip.File{}
		zr, err := zip.NewReader(bytes.NewReader(twemojiZip), int64(len(twemojiZip)))
		if err != nil {
			return
		}
		for _, f := range zr.File {
			emojiFiles[strings.TrimSuffix(f.Name, ".png")] = f
		}
	})
	return emojiFiles
}

// twemojiName follows Twemoji's file naming: code points in hex joined by "-",
// with the emoji presentation selector dropped unless the emoji is a ZWJ sequence.
func twemojiName(runes []rune, keepFE0F bool) string {
	var parts []string
	for _, r := range runes {
		if r == 0xFE0F && !keepFE0F {
			continue
		}
		parts = append(parts, fmt.Sprintf("%x", r))
	}
	return strings.Join(parts, "-")
}

// emojiImage returns the Twemoji picture for the (first) emoji in s, nil if there is none.
func emojiImage(s string) image.Image {
	runes := []rune(strings.TrimSpace(s))
	idx := emojiIndex()
	// longest prefix first, so "👩‍🚀" wins over "👩" and "🎃🦇" still finds "🎃"
	for n := len(runes); n > 0; n-- {
		zwj := strings.ContainsRune(string(runes[:n]), 0x200D)
		for _, name := range []string{twemojiName(runes[:n], zwj), twemojiName(runes[:n], !zwj)} {
			f, ok := idx[name]
			if !ok {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil
			}
			img, err := png.Decode(rc)
			rc.Close()
			if err != nil {
				return nil
			}
			return img
		}
	}
	return nil
}

// emojiHolidays maps season symbols to the hand-drawn accessories, which match the lettering better.
var emojiHolidays = map[string]string{
	"🎃": "halloween", "👻": "halloween", "🦇": "halloween", "🕷": "halloween", "🕸": "halloween",
	"💀": "halloween", "☠": "halloween", "🧙": "halloween", "🧛": "halloween", "🧟": "halloween",
	"🎄": "christmas", "🎅": "christmas", "🤶": "christmas", "🧑‍🎄": "christmas", "⛄": "christmas",
	"☃": "christmas", "🎁": "christmas", "🦌": "christmas", "🔔": "christmas",
	"🐣": "easter", "🐤": "easter", "🐥": "easter", "🥚": "easter", "🐰": "easter", "🐇": "easter",
	"❤": "valentine", "♥": "valentine", "💘": "valentine", "💖": "valentine", "💕": "valentine",
	"💝": "valentine", "💗": "valentine", "💓": "valentine", "💞": "valentine", "😍": "valentine", "🥰": "valentine", "🌹": "valentine",
	"🎆": "newyear", "🎇": "newyear", "🥂": "newyear", "🍾": "newyear", "🎉": "newyear", "🎊": "newyear",
}

// HolidayForEmoji returns the drawn accessory that stands for a season symbol ("" if none fits).
func HolidayForEmoji(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "️", "")
	runes := []rune(s)
	for n := len(runes); n > 0; n-- {
		if kind, ok := emojiHolidays[string(runes[:n])]; ok {
			return kind
		}
	}
	return ""
}

// symbol decorates the logo with a season symbol: a drawn accessory when one fits,
// otherwise the emoji itself as a sticker at the top-right of the lettering.
func (r *renderer) symbol(s string) {
	if kind := HolidayForEmoji(s); kind != "" {
		r.holiday(kind)
		return
	}
	em := emojiImage(s)
	if em == nil || len(r.text.glyphs) == 0 {
		return
	}
	// the right-most letter, on the upper line for two-line names
	last := r.text.glyphs[0]
	for _, g := range r.text.glyphs {
		if g.x1 > last.x1+8 || (g.x1 >= last.x1-8 && g.y0 < last.y0) {
			last = g
		}
	}
	// same scale as the accessories, a little larger because the emoji has its own padding
	size := math.Max(56, math.Min(104, r.text.size*0.9))
	h := size / 2
	// stuck onto the top-right corner of that letter, mostly above it
	cx := math.Min(float64(last.x1)+h*0.35, float64(W)-h-8)
	cy := math.Max(float64(last.y0)-h*0.15, h+8)
	dst := image.Rect(int(cx-h), int(cy-h), int(cx+h), int(cy+h))

	sticker := image.NewRGBA(image.Rect(0, 0, W, H))
	xdraw.CatmullRom.Scale(sticker, dst, em, em.Bounds(), draw.Src, nil)
	mask := image.NewAlpha(sticker.Rect)
	for i := range mask.Pix {
		mask.Pix[i] = sticker.Pix[i*4+3]
	}
	r.paintColor(blur(mask, 3), withAlpha(black, 0.4), 0, 3)
	r.paintColor(dilate(mask, 4), white, 0, 0)
	draw.Draw(r.img, r.img.Bounds(), sticker, image.Point{}, draw.Over)
}
