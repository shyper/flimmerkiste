package logo

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif" // uploads and older stored logos
	_ "image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// MaxUploadSide is the longest side an uploaded logo is reduced to.
const MaxUploadSide = 1280

// ErrUnsupportedImage means the upload is no PNG, JPG, WebP or GIF, or is too large to decode.
var ErrUnsupportedImage = errors.New("unsupported image")

// NormalizeUpload turns an uploaded logo into a PNG whose longest side is at most
// MaxUploadSide. Smaller images keep their size; GIFs keep their first frame.
func NormalizeUpload(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 50_000_000 {
		return nil, ErrUnsupportedImage // also guards against decompression bombs
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupportedImage
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if side := max(w, h); side > MaxUploadSide {
		w = max(1, w*MaxUploadSide/side)
		h = max(1, h*MaxUploadSide/side)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Src, nil)
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, dst); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
