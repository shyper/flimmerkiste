package logo

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestRenderAllStyles(t *testing.T) {
	out := os.Getenv("LOGO_PREVIEW_DIR")
	names := map[string]string{
		"classic": "Komödien Kanal", "retro": "80er Kult", "neon": "Sci-Fi Nacht", "cinema": "Kino Klassiker",
		"cozy": "Sonntagskino", "elegant": "Romantik", "horror": "Grusel TV", "kids": "Kinderkanal",
		"festive": "Weihnachtszauber", "spring": "Frühlingsgefühle", "action": "Action Pur",
		"magic": "Harry Potter Marathon", "epic": "Mittelerde", "fairytale": "Märchenstunde", "space": "Star Wars Saga",
		"western": "Wilder Westen", "comic": "Superhelden", "crime": "Sonntagskrimi", "gothic": "Vampirnacht",
		"nature": "Planet Erde", "ocean": "Karibik Kino", "sport": "Sport Arena", "music": "Musik Hits",
		"news": "Nachrichten 24", "love": "Liebesgrüße", "pixel": "Game Over", "chalk": "Wissen macht Ah", "ice": "Eiszeit",
		"anime": "Anime Abend", "silent": "Stummfilm Matinee", "crayon": "Kinderbuch Kino", "mystery": "Mystery Nacht",
		"bollywood": "Bollywood Masala", "cooking": "Kochen mit Liebe", "atomic": "Atomic Kino", "graffiti": "Street Art",
		"manuscript": "Mittelalter", "vhs": "Videothek",
	}
	for _, s := range Styles {
		if s.ID == "auto" {
			continue
		}
		data, err := Render(Options{Name: names[s.ID], Style: s.ID})
		if err != nil {
			t.Fatalf("%s: %v", s.ID, err)
		}
		if out != "" {
			os.WriteFile(filepath.Join(out, s.ID+".png"), data, 0o644)
		}
	}
	// long name must still fit
	if _, err := Render(Options{Name: "Der ganz besonders lange Sendername für Filmabende", Style: "auto"}); err != nil {
		t.Fatal(err)
	}
}

func TestAutoStyle(t *testing.T) {
	cases := map[string]string{
		"Harry Potter Marathon": "magic", "Herr der Ringe": "epic", "Der Hobbit": "epic",
		"Märchenstunde": "fairytale", "Star Wars Saga": "space", "Wilder Westen": "western",
		"Marvel Universe": "comic", "Sonntagskrimi": "crime", "Dracula & Co": "gothic",
		"Weihnachtszauber": "festive", "Grusel TV": "horror", "80er Kult": "retro",
		"Winterzauber": "ice", "Sommer": "ocean", "Herbst": "nature", "Halloween": "horror",
		"Tierdokus": "nature", "Fußball Klassiker": "sport", "Musicals": "music", "Romantik pur": "love",
		"Nintendo Nostalgie": "pixel", "Ghibli Kino": "anime", "Chaplin & Co": "silent", "Agatha Christie": "mystery",
		"Kochshow": "cooking", "VHS Nostalgie": "vhs", "Robin Hood": "manuscript", "Wissen am Abend": "chalk", "Frozen & Co": "ice", "Piratenkanal": "ocean",
	}
	for name, want := range cases {
		if got := ResolveStyle("auto", name, ""); got != want {
			t.Errorf("%q: got %s, want %s", name, got, want)
		}
	}
	// genres from the channel rules count too
	if got := ResolveStyle("auto", "Sonntagabend", "Fantasy Abenteuer"); got != "magic" {
		t.Errorf("fantasy genre hint: got %s", got)
	}
}

func TestExtras(t *testing.T) {
	out := os.Getenv("LOGO_PREVIEW_DIR")
	for _, h := range []string{"christmas", "halloween", "easter", "newyear", "valentine"} {
		data, err := Render(Options{Name: "Komödien Kanal", Style: "classic", Holiday: h, Number: 3, Tagline: "Lachen garantiert"})
		if err != nil {
			t.Fatal(err)
		}
		if out != "" {
			os.WriteFile(filepath.Join(out, "holiday-"+h+".png"), data, 0o644)
		}
	}
	for _, hue := range []int{0, 120, 240} {
		data, _ := Render(Options{Name: "Sci-Fi Nacht", Style: "neon", Hue: hue, Seed: hue})
		if out != "" {
			os.WriteFile(filepath.Join(out, fmt.Sprintf("hue-%d.png", hue)), data, 0o644)
		}
	}
	// Easter dates: 2026-04-05, 2027-03-28
	if e := EasterSunday(2026); e.Month() != 4 || e.Day() != 5 {
		t.Errorf("easter 2026: %v", e)
	}
	if e := EasterSunday(2027); e.Month() != 3 || e.Day() != 28 {
		t.Errorf("easter 2027: %v", e)
	}
	if h := HolidayAt(time.Date(2026, 12, 24, 0, 0, 0, 0, time.UTC)); h != "christmas" {
		t.Errorf("christmas: %q", h)
	}
	if h := HolidayAt(time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC)); h != "easter" {
		t.Errorf("easter: %q", h)
	}
	if h := HolidayAt(time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)); h != "" {
		t.Errorf("summer: %q", h)
	}
	poster := image.NewRGBA(image.Rect(0, 0, 40, 60))
	for i := range poster.Pix {
		poster.Pix[i] = []uint8{30, 140, 220, 255}[i%4]
	}
	// a blue poster gives a blue accent (it may be made more vivid)
	if c, ok := DominantColor([]image.Image{poster}); !ok {
		t.Error("no dominant colour")
	} else if rgb, _ := parseHex(c); rgb.B < 160 || rgb.B <= rgb.R || rgb.B <= rgb.G {
		t.Errorf("dominant colour of a blue poster: %s", c)
	}
}

func TestRenderCard(t *testing.T) {
	logoPNG, _ := Render(Options{Name: "Harry Potter Marathon", Style: "auto"})
	logoImg, _ := png.Decode(bytes.NewReader(logoPNG))
	poster := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			poster.Set(x, y, color.RGBA{uint8(80 + y/3), 40, uint8(120 + x/3), 255})
		}
	}
	data, err := RenderCard(CardOptions{Width: 1280, Height: 720, Logo: logoImg, Poster: poster, Accent: "#8b5cf6",
		Headline: "Gleich geht's weiter", When: "Um 20:15 Uhr", Title: "Harry Potter und die Kammer des Schreckens", Subtitle: "2002 · Fantasy"})
	if err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("LOGO_PREVIEW_DIR"); out != "" {
		os.WriteFile(filepath.Join(out, "card.png"), data, 0o644)
	}
}

// Shadows, descenders and decorations must not be cut off at the canvas edge.
func TestLogoNotClipped(t *testing.T) {
	for _, c := range [][2]string{{"Funny", "cozy"}, {"Gypsy", "cozy"}, {"Thriller", "mystery"}, {"Funny", "horror"}, {"Polizeiruf", "comic"}} {
		data, err := Render(Options{Name: c[0], Style: c[1]})
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		rgba := image.NewRGBA(img.Bounds())
		draw.Draw(rgba, rgba.Bounds(), img, image.Point{}, draw.Src)
		if touchesEdge(rgba) {
			t.Errorf("%s/%s reaches the edge", c[1], c[0])
		}
	}
}

// TestGoldenLogos compares every style, the holiday accessories and the extras
// pixel by pixel with references in testdata/golden. After an intended change:
//
//	FLIMMERKISTE_GOLDEN_UPDATE=1 go test -run TestGoldenLogos ./internal/logo/
func TestGoldenLogos(t *testing.T) {
	type c struct {
		name string
		o    Options
	}
	cases := []c{
		{"auto_long", Options{Name: "Der ganz besonders lange Sendername für Filmabende", Style: "auto"}},
		{"auto_genre", Options{Name: "Freitagabend", Style: "auto", Hint: "Horror Thriller"}},
		{"extras", Options{Name: "Komödien", Style: "classic", Color: "#3b82f6", Seed: 3, Hue: 40, Tagline: "Lachen garantiert", Number: 7}},
		{"symbol", Options{Name: "Sommerkino", Style: "cozy", Symbol: "🏖️"}},
	}
	for _, h := range []string{"christmas", "halloween", "easter", "newyear", "valentine"} {
		cases = append(cases, c{"holiday_" + h, Options{Name: "Filmabend", Style: "classic", Holiday: h}})
	}
	for _, s := range Styles {
		if s.ID != "auto" {
			cases = append(cases, c{"style_" + s.ID, Options{Name: "Sonntagskino", Style: s.ID}})
		}
	}
	update := os.Getenv("FLIMMERKISTE_GOLDEN_UPDATE") == "1"
	for _, tc := range cases {
		data, err := Render(tc.o)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		path := filepath.Join("testdata", "golden", tc.name+".png")
		if update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (create it with FLIMMERKISTE_GOLDEN_UPDATE=1)", tc.name, err)
		}
		a, err := png.Decode(bytes.NewReader(want))
		if err != nil {
			t.Fatal(err)
		}
		b, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if a.Bounds() != b.Bounds() {
			t.Errorf("%s: size %v, want %v", tc.name, b.Bounds(), a.Bounds())
			continue
		}
		n := 0
		for y := a.Bounds().Min.Y; y < a.Bounds().Max.Y; y++ {
			for x := a.Bounds().Min.X; x < a.Bounds().Max.X; x++ {
				if color.NRGBAModel.Convert(a.At(x, y)) != color.NRGBAModel.Convert(b.At(x, y)) {
					n++
				}
			}
		}
		if n > 0 {
			out := filepath.Join(t.TempDir(), tc.name+".png")
			_ = os.WriteFile(out, data, 0o644)
			t.Errorf("%s: %d pixels differ; got written to %s", tc.name, n, out)
		}
	}
}

// TestRenderOddNames renders names that once hung, crashed or came out empty
// in every style: each finishes quickly with visible ink or reports ErrNoGlyphs.
func TestRenderOddNames(t *testing.T) {
	if testing.Short() {
		t.Skip("renders every style several times (minutes under the race detector)")
	}
	long := ""
	for i := 0; i < 200; i++ {
		long += "Wort "
	}
	names := []string{"Москва ТВ", "東京チャンネル", "🙂😀", "!", long, string(bytes.Repeat([]byte("M"), 700))}
	for _, s := range Styles {
		t.Run(s.ID, func(t *testing.T) {
			t.Parallel()
			oddNames(t, s.ID, names)
		})
	}
}

func oddNames(t *testing.T, style string, names []string) {
	for _, name := range names {
		done := make(chan error, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- fmt.Errorf("panic: %v", r)
				}
			}()
			data, err := Render(Options{Name: name, Style: style})
			if err == nil && visiblePixels(data) == 0 {
				err = fmt.Errorf("nothing visible")
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil && err != ErrNoGlyphs {
				t.Errorf("%.20q: %v", name, err)
			}
		case <-time.After(20 * time.Second):
			t.Fatalf("%.20q: no logo after 20s", name)
		}
	}
}

// visiblePixels counts the clearly visible pixels of a PNG (0 if it is none).
func visiblePixels(data []byte) int {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x0800 {
				n++
			}
		}
	}
	return n
}

// TestHexLiterals: hex() draws nothing for a typo, so every colour written in
// the styles must parse.
func TestHexLiterals(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`hex\("([^"]*)"\)`)
	n := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllSubmatch(src, -1) {
			n++
			if _, ok := parseHex(string(m[1])); !ok {
				t.Errorf("%s: invalid colour %q", f, m[1])
			}
		}
	}
	if n < 100 {
		t.Fatalf("only %d colours found", n)
	}
}
