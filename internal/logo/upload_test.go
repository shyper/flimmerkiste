package logo

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

func encodePNG(t *testing.T, img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodePNG(t *testing.T, data []byte) image.Image {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("result is no PNG: %v", err)
	}
	return img
}

func TestNormalizeUploadScalesDown(t *testing.T) {
	out, err := NormalizeUpload(encodePNG(t, image.NewNRGBA(image.Rect(0, 0, 3000, 1500))))
	if err != nil {
		t.Fatal(err)
	}
	if b := decodePNG(t, out).Bounds(); b.Dx() != MaxUploadSide || b.Dy() != MaxUploadSide/2 {
		t.Fatalf("size = %v, want %dx%d", b.Size(), MaxUploadSide, MaxUploadSide/2)
	}
}

func TestNormalizeUploadKeepsSmallAndTransparent(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	src.Set(10, 10, color.NRGBA{255, 0, 0, 255})
	out, err := NormalizeUpload(encodePNG(t, src))
	if err != nil {
		t.Fatal(err)
	}
	img := decodePNG(t, out)
	if img.Bounds().Dx() != 400 || img.Bounds().Dy() != 200 {
		t.Fatalf("size changed to %v", img.Bounds().Size())
	}
	if _, _, _, a := img.At(100, 100).RGBA(); a != 0 {
		t.Fatal("transparency lost")
	}
	if r, _, _, _ := img.At(10, 10).RGBA(); r>>8 != 255 {
		t.Fatal("pixel content lost")
	}
}

func TestNormalizeUploadConvertsGIF(t *testing.T) {
	pal := image.NewPaletted(image.Rect(0, 0, 64, 32), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, pal, nil); err != nil {
		t.Fatal(err)
	}
	out, err := NormalizeUpload(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	decodePNG(t, out)
}

func TestNormalizeUploadRejectsGarbage(t *testing.T) {
	if _, err := NormalizeUpload([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>")); err != ErrUnsupportedImage {
		t.Fatalf("err = %v, want ErrUnsupportedImage", err)
	}
}
