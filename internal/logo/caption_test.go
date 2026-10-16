package logo

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func TestRenderCaption(t *testing.T) {
	poster := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for _, o := range []CaptionOptions{
		{Height: 720, Accent: "#e5a00d", Poster: poster, Kicker: "Heute um 22:00 Uhr", Title: "Zurück in die Zukunft", Sub: "1985 · Abenteuer"},
		{Height: 1080, Title: "Ein sehr, sehr langer Filmtitel, der auf keinen Fall in eine einzige Bauchbinde passen kann, egal wie"},
	} {
		data, err := RenderCaption(o)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		b := img.Bounds()
		if b.Dx() > o.Height*16/9*2/3 || b.Dy() > o.Height*3/10 {
			t.Errorf("caption %v too large for a %dp frame", b, o.Height)
		}
		// transparent margin around the panel (room for the shadow)
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Error("corner not transparent")
		}
		if _, _, _, a := img.At(b.Dx()/2, b.Dy()/2).RGBA(); a < 0xc000 {
			t.Error("panel not opaque enough")
		}
	}
}

// TestRenderCaptionPages: the pages of a changing card have one size, smaller
// than the caption of a trailer.
func TestRenderCaptionPages(t *testing.T) {
	pages, err := RenderCaptionPages([]CaptionOptions{
		{Height: 720, Scale: 0.8, Kicker: "Im Anschluss", Title: "Ghostbusters"},
		{Height: 720, Scale: 0.8, Accent: "#3b82f6", Kicker: "21:00 Uhr · Science-Fiction", Title: "Zurück in die Zukunft"},
		{Height: 720, Scale: 0.8, Kicker: "21:45 Uhr · Familienkino", Title: "E.T."},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range pages {
		if p.Bounds() != pages[0].Bounds() {
			t.Errorf("page %d: %v, want %v like the first", i, p.Bounds(), pages[0].Bounds())
		}
	}
	data, err := RenderCaption(CaptionOptions{Height: 720, Kicker: "Im Anschluss", Title: "Ghostbusters"})
	if err != nil {
		t.Fatal(err)
	}
	full, _ := png.Decode(bytes.NewReader(data))
	if pages[0].Bounds().Dy() >= full.Bounds().Dy() {
		t.Errorf("card %v not smaller than the caption %v", pages[0].Bounds(), full.Bounds())
	}
}
