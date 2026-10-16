// Package logo renders channel logos: the full channel name set in a
// decorative font with style-specific effects (glow, gradients, outlines …).
package logo

import (
	"bytes"
	"embed"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"math/rand"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

//go:embed fonts/*.ttf
var fontFS embed.FS

const (
	W = 640
	H = 360
)

type StyleInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// Styles is the catalogue shown in the UI. "auto" picks one from the channel name and theme.
var Styles = []StyleInfo{
	{"auto", "Automatisch", "Passend zu Name und Inhalt"},
	{"classic", "Klassik", "Kräftige Schrift auf farbigem Badge"},
	{"retro", "Retro 80er", "Sonnenuntergangs-Verlauf mit Streifen"},
	{"neon", "Neon", "Leuchtröhren-Glow bei Nacht"},
	{"cinema", "Kino", "Gold auf Schwarz im Art-déco-Stil"},
	{"cozy", "Gemütlich", "Handschrift mit langem Schatten"},
	{"elegant", "Elegant", "Geschwungene goldene Schreibschrift"},
	{"horror", "Horror", "Blutrot mit Tropfen"},
	{"kids", "Kinder", "Bunte Buchstaben mit dicker Kontur"},
	{"festive", "Festlich", "Rot-Gold mit funkelnden Sternen"},
	{"spring", "Frühling", "Pastell-Verlauf mit Blüten"},
	{"action", "Action", "Feuriger Verlauf mit Speed-Lines"},
	{"magic", "Zauberei", "Goldschrift mit magischer Aura und Funkenspur"},
	{"epic", "Episch", "Geschmiedetes Gold mit glühender Glut"},
	{"fairytale", "Märchen", "Storybook-Schrift auf Pergament-Banner"},
	{"space", "Weltraum", "Chrom-Schrift mit Sternen und Lens-Flare"},
	{"western", "Western", "Staubige Lettern, Abendsonne und Sheriff-Sterne"},
	{"comic", "Comic", "Explosions-Zacken mit Rasterpunkten"},
	{"crime", "Krimi", "Schreibmaschine auf Polizei-Absperrband"},
	{"gothic", "Gothic", "Silberfraktur, Vollmond und Fledermäuse"},
	{"nature", "Natur", "Bergsilhouette im Abendlicht mit Zugvögeln"},
	{"ocean", "Ozean", "Wellen, Luftblasen und Türkis"},
	{"sport", "Sport", "College-Trikot mit doppelter Kontur"},
	{"music", "Musik", "Neon-Linien über einem Equalizer"},
	{"news", "Nachrichten", "Schräger Balken wie eine Bauchbinde"},
	{"love", "Liebe", "Geschwungene Schrift mit Herzen"},
	{"pixel", "Pixel", "8-Bit-Arcade mit Bildschirmzeilen"},
	{"chalk", "Tafel", "Kreide auf der Schultafel"},
	{"ice", "Eis", "Frostschrift mit Eiszapfen und Schneeflocken"},
	{"anime", "Anime", "Pinselschrift vor roter Sonne"},
	{"silent", "Stummfilm", "Art-déco-Zwischentitel mit Filmkörnung"},
	{"crayon", "Kinderbuch", "Wachsmalstift mit Sonne, Wolke und Wiese"},
	{"mystery", "Mystery", "Nebel, Lupe und Fragezeichen"},
	{"bollywood", "Bollywood", "Magenta und Safran auf goldenem Mandala"},
	{"cooking", "Kochen", "Retro-Diner-Schild mit Besteck"},
	{"atomic", "Atomzeitalter", "50er-Jahre-Space-Age mit Ringplanet"},
	{"graffiti", "Graffiti", "Sprühfarbe mit Klecksen und Tropfen"},
	{"manuscript", "Pergament", "Initiale wie in einer alten Handschrift"},
	{"vhs", "VHS", "Videokassetten-Etikett mit Leih-Aufkleber"},
}

var styleFonts = map[string]string{
	"classic": "BebasNeue-Regular.ttf",
	"retro":   "Righteous-Regular.ttf",
	"neon":    "Audiowide-Regular.ttf",
	"cinema":  "AbrilFatface-Regular.ttf",
	"cozy":    "Pacifico-Regular.ttf",
	"elegant": "GreatVibes-Regular.ttf",
	"horror":  "Creepster-Regular.ttf",
	"kids":    "LuckiestGuy-Regular.ttf",
	"festive": "BerkshireSwash-Regular.ttf",
	"spring":  "EmilysCandy-Regular.ttf",
	"action":  "Bungee-Regular.ttf",

	"magic":     "Almendra-Bold.ttf",
	"epic":      "CinzelDecorative-Bold.ttf",
	"fairytale": "UncialAntiqua-Regular.ttf",
	"space":     "Michroma-Regular.ttf",
	"western":   "Rye-Regular.ttf",
	"comic":     "Bangers-Regular.ttf",
	"crime":     "SpecialElite-Regular.ttf",
	"gothic":    "UnifrakturMaguntia-Book.ttf",

	"nature": "ZillaSlab-Bold.ttf",
	"ocean":  "LilitaOne-Regular.ttf",
	"sport":  "Graduate-Regular.ttf",
	"music":  "Monoton-Regular.ttf",
	"news":   "Anton-Regular.ttf",
	"love":   "Satisfy-Regular.ttf",
	"pixel":  "PressStart2P-Regular.ttf",
	"chalk":  "CabinSketch-Bold.ttf",
	"ice":    "BowlbyOneSC-Regular.ttf",

	"anime":      "KaushanScript-Regular.ttf",
	"silent":     "Limelight-Regular.ttf",
	"crayon":     "IndieFlower-Regular.ttf",
	"mystery":    "IMFeENsc28P.ttf",
	"bollywood":  "RozhaOne-Regular.ttf",
	"cooking":    "Lobster-Regular.ttf",
	"atomic":     "AtomicAge-Regular.ttf",
	"graffiti":   "SedgwickAveDisplay-Regular.ttf",
	"manuscript": "MedievalSharp.ttf",
	"vhs":        "PermanentMarker-Regular.ttf",
}

// bleedStyles run decorations (tape, speed lines, stars, snow) to the canvas edge on purpose.
var bleedStyles = map[string]bool{"crime": true, "action": true, "anime": true, "space": true,
	"festive": true, "ice": true, "bollywood": true, "fairytale": true}

var uppercaseStyles = map[string]bool{"retro": true, "action": true, "kids": true, "classic": true, "neon": true,
	"space": true, "western": true, "comic": true, "crime": true, "sport": true, "news": true, "pixel": true, "music": true}

type Options struct {
	Name  string // channel name, written out in full
	Style string // style id or "auto"/""
	Color string // optional accent colour "#rrggbb"
	Hint  string // extra words used by "auto" (genres, current season, theme)

	Seed    int    // variant: re-arranges stars, waves, hearts … (0 = default layout)
	Hue     int    // colour theme: rotates the hue of the artwork by this many degrees
	Tagline string // optional small second line ("Movies for the whole family")
	Number  int    // channel number shown as a small badge when > 0
	Holiday string // seasonal accessory: christmas, halloween, easter, newyear, valentine ("" = none)
	Symbol  string // season emoji; replaces Holiday (drawn accessory if one fits, else the emoji as a sticker)
}

// weakWords describe a mood or time slot rather than a genre.
var weakWords = map[string]bool{
	"sonntag": true, "gemütlich": true, "cozy": true, "nacht": true, "night": true, "club": true,
	"future": true, "film": true, "movie": true, "kino": true, "cinema": true, "sommer": true,
	"summer": true, "winter": true, "herbst": true, "mai": true, "serien": true, "legende": true,
	"klassiker": true, "nostalgie": true, "koch": true, "atom": true, "street": true,
	"date": true, "band": true, "info": true, "uni": true, "golf": true, "herz": true, "tiere": true, "urlaub": true, "ferien": true,
}

// ResolveStyle maps "auto" to a concrete style based on name and hint keywords.
func ResolveStyle(style, name, hint string) string {
	if _, ok := styleFonts[style]; ok {
		return style
	}
	text := strings.ToLower(name + " " + hint)
	keywords := []struct {
		style string
		words []string
	}{
		{"festive", []string{"weihnacht", "christmas", "xmas", "advent", "festtag", "silvester"}},
		{"ice", []string{"winter", "winterzauber", "frozen", "eiskönig", "schnee", "snow", "arktis", "antarktis", "ski", "frost", "eiszeit", "ice age"}},
		{"nature", []string{"natur", "nature", "doku", "dokumentation", "tierwelt", "tiere", "wildlife", "wildnis", "planet erde", "wald", "berge", "herbst", "outdoor"}},
		{"ocean", []string{"meer", "ozean", "ocean", "strand", "beach", "insel", "surf", "karibik", "pirat", "unterwasser", "sommer", "summer", "urlaub", "ferien"}},
		{"sport", []string{"sport", "fußball", "fussball", "football", "boxen", "boxing", "basketball", "baseball", "olymp", "racing", "rennen", "formel", "tennis", "golf"}},
		{"music", []string{"musik", "music", "konzert", "concert", "musical", "rock", "pop", "hits", "disco", "dance", "tanz", "band", "jazz"}},
		{"news", []string{"nachrichten", "news", "info", "aktuell", "reportage", "politik", "zeitgeschehen", "journal", "magazin"}},
		{"love", []string{"romant", "romanze", "liebe", "love", "herz", "valentin", "hochzeit", "wedding", "date"}},
		{"pixel", []string{"game", "gaming", "videospiel", "arcade", "pixel", "8-bit", "8bit", "nintendo", "mario", "zelda", "pokemon", "nerd", "retro-game"}},
		{"anime", []string{"anime", "manga", "ghibli", "japan", "tokio", "tokyo", "samurai", "ninja"}},
		{"silent", []string{"stummfilm", "schwarz-weiß", "schwarzweiß", "schwarz weiss", "chaplin", "klassiker", "golden age", "nostalgie", "1920", "20er"}},
		{"crayon", []string{"vorschule", "kleinkind", "sandmann", "kita", "kindergarten", "bilderbuch", "kinderbuch", "kinderzimmer"}},
		{"mystery", []string{"mystery", "rätsel", "raetsel", "geheimnis", "akte x", "unerklärlich", "verschwörung", "whodunit", "agatha"}},
		{"bollywood", []string{"bollywood", "indien", "india", "masala", "tollywood"}},
		{"cooking", []string{"kochen", "koch", "küche", "kueche", "cooking", "food", "rezept", "backen", "chef", "gourmet", "dinner"}},
		{"atomic", []string{"atomic", "atom", "b-movie", "50er", "50s", "ufo", "retro sci", "invasion"}},
		{"graffiti", []string{"graffiti", "street", "urban", "hip-hop", "rap", "skate", "ghetto", "bronx"}},
		{"manuscript", []string{"mittelalter", "historisch", "historie", "geschichte", "kloster", "chronik", "robin hood", "artus"}},
		{"vhs", []string{"vhs", "videothek", "kassette", "videoabend", "leihfilm", "videotheke"}},
		{"chalk", []string{"wissen", "bildung", "lernen", "schule", "school", "uni", "quiz", "wissenschaft", "experiment", "lehr"}},
		{"epic", []string{"herr der ringe", "hobbit", "mittelerde", "middle-earth", "ringe", "epos", "episch", "epic", "saga", "ritter", "schlacht", "schwert", "wikinger", "thron", "gladiator", "legende"}},
		{"magic", []string{"harry potter", "hogwarts", "zauber", "magie", "magic", "magisch", "hexer", "wizard", "tierwesen", "fantasy"}},
		{"fairytale", []string{"märchen", "maerchen", "narnia", "prinzessin", "princess", "fairy", "grimm", "es war einmal"}},
		{"gothic", []string{"vampir", "dracula", "gothic", "gruft", "twilight", "düster", "finster"}},
		{"horror", []string{"horror", "grusel", "halloween", "zombie", "schock", "creep", "fright"}},
		{"space", []string{"star wars", "star trek", "weltraum", "space", "galax", "alien", "sci-fi", "scifi", "science", "planet"}},
		{"western", []string{"western", "cowboy", "wilder westen", "prärie", "sheriff", "outlaw"}},
		{"comic", []string{"comic", "superheld", "superhero", "marvel", "avengers", "anime", "manga", "batman", "spider", "x-men"}},
		{"crime", []string{"krimi", "crime", "tatort", "detektiv", "mord", "noir", "polizei", "ermittl", "sherlock", "verbrechen"}},
		{"kids", []string{"kinder", "kids", "cartoon", "zeichentrick", "disney", "pixar", "familie", "family", "anima"}},
		{"spring", []string{"frühling", "fruehling", "ostern", "easter", "spring", "blüte", "mai"}},
		{"retro", []string{"80er", "80s", "90er", "90s", "retro", "synth", "vhs"}},
		{"neon", []string{"neon", "cyber", "nacht", "night", "future", "club"}},
		{"action", []string{"action", "sport", "thriller", "explosion", "speed"}},
		{"elegant", []string{"drama", "klassik", "classic", "oper", "opera", "ballett", "eleganz", "gala"}},
		{"cinema", []string{"kino", "cinema", "film", "movie", "oscar", "blockbuster", "premiere"}},
		{"cozy", []string{"gemütlich", "cozy", "sonntag", "komödie", "comedy", "sitcom", "serien"}},
	}
	// The most specific (longest) keyword wins, so "Star Wars Saga" is space, not epic.
	// Mood words only count when no genre keyword matched ("Sonntagskrimi" is crime).
	best, bestScore := "", 0
	for _, k := range keywords {
		for _, w := range k.words {
			score := len(w)
			if weakWords[w] {
				score = 1
			}
			if score > bestScore && strings.Contains(text, w) {
				best, bestScore = k.style, score
			}
		}
	}
	if best != "" {
		return best
	}
	fallback := []string{"classic", "retro", "neon", "cinema", "cozy", "action"}
	return fallback[hash(name)%uint32(len(fallback))]
}

var (
	fontsMu sync.Mutex
	fonts   = map[string]*opentype.Font{}
)

func loadFont(file string) (*opentype.Font, error) {
	fontsMu.Lock()
	defer fontsMu.Unlock()
	if f, ok := fonts[file]; ok {
		return f, nil
	}
	data, err := fontFS.ReadFile("fonts/" + file)
	if err != nil {
		return nil, err
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, err
	}
	fonts[file] = f
	return f, nil
}

// Render produces a transparent PNG logo.
func Render(o Options) ([]byte, error) {
	style := ResolveStyle(o.Style, o.Name, o.Hint)
	name := strings.TrimSpace(o.Name)
	if name == "" {
		name = "Flimmerkiste"
	}
	if uppercaseStyles[style] {
		name = strings.ToUpper(name)
	}
	f, err := loadFont(styleFonts[style])
	if err != nil {
		return nil, err
	}
	accent, ok := parseHex(o.Color)
	if !ok {
		accent = defaultAccent(style, o.Name)
	}
	seed := int64(hash(o.Name)) + int64(o.Seed)*7919

	pad := 40
	switch style {
	case "classic", "cinema", "crime":
		pad = 64 // text sits inside a badge / on a tape
	case "fairytale", "comic":
		pad = 96 // room for the ribbon tails / burst spikes
	case "magic", "gothic", "western", "pixel", "sport":
		pad = 56 // room for trail, moon and bats, stars, coins
	case "news", "chalk":
		pad = 72 // text sits on a bar / board
	case "nature", "ocean", "music", "love", "ice":
		pad = 50 // room for mountains, waves, equaliser, hearts, icicles
	case "anime", "crayon", "atomic", "graffiti", "bollywood", "mystery":
		pad = 60
	case "silent", "manuscript":
		pad = 76 // text sits on a card / parchment
	case "cooking", "vhs":
		pad = 84 // sign / cassette label with extras
	}
	// A tagline takes the bottom of the canvas; the name moves up a little.
	maxH, center := H-2*pad, float64(H)/2
	if strings.TrimSpace(o.Tagline) != "" {
		maxH -= taglineH
		center -= taglineH / 2
	}
	// Decorations (shadows, drips, a magnifying glass, the sun ...) may reach past the
	// text; when they would be cut off at the canvas edge, the name shrinks a little.
	var img *image.RGBA
	var r renderer
	for scale := 1.0; ; scale *= 0.9 {
		t, err := layoutText(f, name, int(float64(W-2*pad)*scale), int(float64(maxH)*scale), center)
		if err != nil {
			return nil, err
		}
		img = image.NewRGBA(image.Rect(0, 0, W, H))
		r = renderer{img: img, text: t, accent: accent, rng: rand.New(rand.NewSource(seed))}
		styleFuncs[style](&r)
		if bleedStyles[style] || scale < 0.6 || !touchesEdge(img) {
			break
		}
	}
	if o.Hue%360 != 0 {
		rotateHue(img, float64(o.Hue%360))
	}
	if err := r.tagline(o.Tagline, center); err != nil {
		return nil, err
	}
	if strings.TrimSpace(o.Symbol) != "" {
		r.symbol(o.Symbol)
	} else {
		r.holiday(o.Holiday)
	}
	if o.Number > 0 {
		if err := r.numberBadge(o.Number); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.DefaultCompression}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ---- text layout ----

type glyphSpan struct{ x0, x1, y0, y1 int }

type textLayout struct {
	mask   *image.Alpha
	bounds image.Rectangle // tight-ish bounds of the text block
	glyphs []glyphSpan     // one per visible rune, for per-letter colouring
	size   float64
}

// layoutText picks the line split and font size that make the name as large as possible.
func layoutText(f *opentype.Font, name string, maxW, maxH int, centerY float64) (*textLayout, error) {
	const probe = 100.0
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: probe, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	m := face.Metrics()
	lineH := float64(m.Ascent+m.Descent) / 64 * 0.92
	words := strings.Fields(name)
	bestSize := 0.0
	var bestLines []string
	for n := 1; n <= 3 && n <= len(words); n++ {
		lines := splitBalanced(words, n, func(s string) float64 { return measure(face, s) })
		widest := 0.0
		for _, l := range lines {
			widest = math.Max(widest, measure(face, l))
		}
		size := math.Min(float64(maxW)/widest*probe, float64(maxH)/(lineH*float64(n))*probe)
		size = math.Min(size, 190)
		// Prefer fewer lines unless splitting gains noticeably.
		if size > bestSize*1.12 || bestLines == nil {
			bestSize, bestLines = size, lines
		}
	}
	face.Close()

	face, err = opentype.NewFace(f, &opentype.FaceOptions{Size: bestSize, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	defer face.Close()
	m = face.Metrics()
	asc, desc := float64(m.Ascent)/64, float64(m.Descent)/64
	lh := (asc + desc) * 0.92
	blockH := lh*float64(len(bestLines)-1) + asc + desc*0.5
	top := centerY - blockH/2 + asc
	layout := func(top float64) *textLayout {
		mask := image.NewAlpha(image.Rect(0, 0, W, H))
		t := &textLayout{mask: mask, size: bestSize}
		drawLines(t, face, bestLines, top, asc, desc, lh)
		t.bounds = alphaBounds(mask)
		return t
	}
	t := layout(top)
	// Centre what is actually inked: script fonts carry deep descenders and little cap
	// height, so the metrics alone push the name down and cut off its tails.
	inkTop, inkBottom := float64(t.bounds.Min.Y), float64(t.bounds.Max.Y)
	if dy := math.Round(centerY - (inkTop+inkBottom)/2); dy != 0 && inkTop+dy >= 0 && inkBottom+dy <= H {
		t = layout(top + dy)
	}
	return t, nil
}

// drawLines writes the centred lines into t.mask, the first baseline at y.
func drawLines(t *textLayout, face font.Face, lines []string, y, asc, desc, lh float64) {
	d := &font.Drawer{Dst: t.mask, Src: image.Opaque, Face: face}
	for _, line := range lines {
		x := (float64(W) - measure(face, line)) / 2
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
		prev := rune(-1)
		for _, ch := range line {
			if prev >= 0 {
				d.Dot.X += face.Kern(prev, ch)
			}
			x0 := d.Dot.X.Round()
			d.DrawString(string(ch))
			if !unicode.IsSpace(ch) {
				t.glyphs = append(t.glyphs, glyphSpan{x0, d.Dot.X.Round(), int(y - asc), int(y + desc)})
			}
			prev = ch
		}
		y += lh
	}
}

// touchesEdge reports whether clearly visible paint reaches the border of the canvas.
func touchesEdge(img *image.RGBA) bool {
	const minAlpha = 48
	for x := 0; x < W; x++ {
		if img.Pix[x*4+3] > minAlpha || img.Pix[(H-1)*img.Stride+x*4+3] > minAlpha {
			return true
		}
	}
	for y := 0; y < H; y++ {
		if img.Pix[y*img.Stride+3] > minAlpha || img.Pix[y*img.Stride+(W-1)*4+3] > minAlpha {
			return true
		}
	}
	return false
}

func measure(face font.Face, s string) float64 {
	return float64(font.MeasureString(face, s)) / 64
}

// splitBalanced splits words into n lines minimising the widest line.
func splitBalanced(words []string, n int, width func(string) float64) []string {
	if n <= 1 {
		return []string{strings.Join(words, " ")}
	}
	best := math.Inf(1)
	var bestSplit []string
	var rec func(start, left int, acc []string)
	rec = func(start, left int, acc []string) {
		if left == 1 {
			lines := append(append([]string{}, acc...), strings.Join(words[start:], " "))
			widest := 0.0
			for _, l := range lines {
				widest = math.Max(widest, width(l))
			}
			if widest < best {
				best, bestSplit = widest, lines
			}
			return
		}
		for end := start + 1; end <= len(words)-left+1; end++ {
			rec(end, left-1, append(acc, strings.Join(words[start:end], " ")))
		}
	}
	rec(0, n, nil)
	return bestSplit
}

func alphaBounds(a *image.Alpha) image.Rectangle {
	b := a.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := a.Pix[(y-b.Min.Y)*a.Stride:]
		for x := b.Min.X; x < b.Max.X; x++ {
			if row[x-b.Min.X] > 20 {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if minX > maxX {
		return image.Rect(0, 0, W, H)
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// ---- colour helpers ----

func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(s)))
	return h.Sum32()
}

func parseHex(s string) (color.RGBA, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return color.RGBA{}, false
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(s, "%02x%02x%02x", &r, &g, &b); err != nil {
		return color.RGBA{}, false
	}
	return color.RGBA{r, g, b, 255}, true
}

func hex(s string) color.RGBA { c, _ := parseHex(s); return c }

func defaultAccent(style, name string) color.RGBA {
	palettes := map[string][]string{
		"classic": {"#2563eb", "#dc2626", "#059669", "#7c3aed", "#ea580c", "#0891b2", "#db2777"},
		"neon":    {"#ff2bd6", "#00e5ff", "#7cff4f", "#ffb800", "#a855ff"},
		"cozy":    {"#c2410c", "#9d174d", "#1d4ed8", "#047857", "#6d28d9"},
	}
	if p, ok := palettes[style]; ok {
		return hex(p[hash(name)%uint32(len(p))])
	}
	return hex("#ff4d6d")
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), l(a.A, b.A)}
}

func withAlpha(c color.RGBA, a float64) color.RGBA {
	// image/draw expects premultiplied colours.
	return color.RGBA{uint8(float64(c.R) * a), uint8(float64(c.G) * a), uint8(float64(c.B) * a), uint8(255 * a)}
}

// gradient builds a vertical gradient across rect from evenly spaced stops.
func gradient(rect image.Rectangle, stops ...color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	h := float64(rect.Dy())
	for y := 0; y < H; y++ {
		t := (float64(y - rect.Min.Y)) / h
		t = math.Max(0, math.Min(1, t))
		seg := t * float64(len(stops)-1)
		i := int(seg)
		if i >= len(stops)-1 {
			i = len(stops) - 2
		}
		c := mix(stops[i], stops[i+1], seg-float64(i))
		for x := 0; x < W; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// ---- mask operations ----

// visibleBounds returns the visible part of img (alpha above a faint threshold).
func visibleBounds(img image.Image) image.Rectangle {
	b := img.Bounds()
	out := image.Rectangle{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x0800 {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

// trimAlpha cuts the transparent margin off a logo, so it can be placed by what
// is visible (rendered logos sit in the middle of a wide canvas). nil stays nil.
func trimAlpha(img image.Image) image.Image {
	if img == nil {
		return nil
	}
	b := visibleBounds(img)
	if b.Empty() || b == img.Bounds() {
		return img
	}
	if sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(b)
	}
	return img
}

func cloneAlpha(a *image.Alpha) *image.Alpha {
	b := image.NewAlpha(a.Rect)
	copy(b.Pix, a.Pix)
	return b
}

// blur is a three-pass box blur, a cheap approximation of a gaussian.
func blur(a *image.Alpha, r int) *image.Alpha {
	if r <= 0 {
		return cloneAlpha(a)
	}
	out := cloneAlpha(a)
	tmp := make([]int, W*H)
	for pass := 0; pass < 3; pass++ {
		for y := 0; y < H; y++ {
			sum := 0
			for x := -r; x <= r; x++ {
				sum += int(out.Pix[y*out.Stride+clamp(x, 0, W-1)])
			}
			for x := 0; x < W; x++ {
				tmp[y*W+x] = sum / (2*r + 1)
				sum += int(out.Pix[y*out.Stride+clamp(x+r+1, 0, W-1)]) - int(out.Pix[y*out.Stride+clamp(x-r, 0, W-1)])
			}
		}
		for x := 0; x < W; x++ {
			sum := 0
			for y := -r; y <= r; y++ {
				sum += tmp[clamp(y, 0, H-1)*W+x]
			}
			for y := 0; y < H; y++ {
				out.Pix[y*out.Stride+x] = uint8(sum / (2*r + 1))
				sum += tmp[clamp(y+r+1, 0, H-1)*W+x] - tmp[clamp(y-r, 0, H-1)*W+x]
			}
		}
	}
	return out
}

// dilate grows the mask by roughly r pixels with a soft edge (for outlines).
func dilate(a *image.Alpha, r int) *image.Alpha {
	b := blur(a, r/2+1)
	for i, v := range b.Pix {
		b.Pix[i] = uint8(clamp(int(v)*5, 0, 255))
	}
	return b
}

func scaleAlpha(a *image.Alpha, f float64) *image.Alpha {
	b := cloneAlpha(a)
	for i, v := range b.Pix {
		b.Pix[i] = uint8(math.Min(255, float64(v)*f))
	}
	return b
}

func union(a, b *image.Alpha) {
	for i, v := range b.Pix {
		if v > a.Pix[i] {
			a.Pix[i] = v
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---- renderer ----

type renderer struct {
	img    *image.RGBA
	text   *textLayout
	accent color.RGBA
	rng    *rand.Rand
}

func (r *renderer) paint(mask *image.Alpha, src image.Image, dx, dy int) {
	draw.DrawMask(r.img, r.img.Bounds(), src, image.Point{}, mask, image.Point{-dx, -dy}, draw.Over)
}

func (r *renderer) paintColor(mask *image.Alpha, c color.RGBA, dx, dy int) {
	r.paint(mask, image.NewUniform(c), dx, dy)
}

func roundedRect(rect image.Rectangle, radius float32) *image.Alpha {
	z := vector.NewRasterizer(W, H)
	x0, y0, x1, y1 := float32(rect.Min.X), float32(rect.Min.Y), float32(rect.Max.X), float32(rect.Max.Y)
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
	m := image.NewAlpha(image.Rect(0, 0, W, H))
	z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

func star(z *vector.Rasterizer, cx, cy, size float32) {
	// four-pointed sparkle
	in := size * 0.28
	z.MoveTo(cx, cy-size)
	z.QuadTo(cx+in*0.3, cy-in*0.3, cx+size, cy)
	z.QuadTo(cx+in*0.3, cy+in*0.3, cx, cy+size)
	z.QuadTo(cx-in*0.3, cy+in*0.3, cx-size, cy)
	z.QuadTo(cx-in*0.3, cy-in*0.3, cx, cy-size)
	z.ClosePath()
}

func circle(z *vector.Rasterizer, cx, cy, rad float32) {
	const k = 0.5523
	z.MoveTo(cx+rad, cy)
	z.CubeTo(cx+rad, cy+rad*k, cx+rad*k, cy+rad, cx, cy+rad)
	z.CubeTo(cx-rad*k, cy+rad, cx-rad, cy+rad*k, cx-rad, cy)
	z.CubeTo(cx-rad, cy-rad*k, cx-rad*k, cy-rad, cx, cy-rad)
	z.CubeTo(cx+rad*k, cy-rad, cx+rad, cy-rad*k, cx+rad, cy)
	z.ClosePath()
}

func rasterMask(fn func(z *vector.Rasterizer)) *image.Alpha {
	z := vector.NewRasterizer(W, H)
	fn(z)
	m := image.NewAlpha(image.Rect(0, 0, W, H))
	z.Draw(m, m.Bounds(), image.Opaque, image.Point{})
	return m
}

func (r *renderer) outlineWidth(f float64) int {
	return int(math.Max(3, r.text.size*f))
}
