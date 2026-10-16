package schedule

import (
	"github.com/shyper/flimmerkiste/internal/i18n"
	"github.com/shyper/flimmerkiste/internal/rules"
)

func any_(rs ...rules.Rule) *rules.Group { return &rules.Group{Match: "any", Rules: rs} }

func r(field, op string, values ...string) rules.Rule {
	return rules.Rule{Field: field, Op: op, Values: values}
}

// SeasonalPreset is a ready-made calendar for a "seasonal" channel, with the
// season names in l. Plex has
// no "Christmas" genre, so festive titles are found via title, summary,
// collections and labels. Switching a phase to AI selection is more accurate.
func SeasonalPreset(l i18n.Lang) Config {
	c := Config{
		Order:            "shuffle",
		KeepEpisodeOrder: true,
		NoRepeatHours:    72,
		Transcode:        "default",
		Seasons: []Season{
			{
				ID: "christmas", Name: i18n.T(l, "season.christmas"), Emoji: "🎄", From: "12-01", To: "12-26",
				Source: Source{Type: "rules", Rules: any_(
					r("title", "contains", "Weihnacht", "Christmas", "Xmas", "Santa", "Nikolaus", "Grinch", "Rentier", "Elf", "Polar Express", "Kevin"),
					r("collection", "contains", "Weihnacht", "Christmas"),
					r("label", "contains", "Weihnacht", "Christmas"),
					r("summary", "contains", "Weihnacht", "Christmas", "Heiligabend", "Christkind", "Weihnachtsmann"),
				)},
			},
			{
				ID: "winter", Name: i18n.T(l, "season.winter"), Emoji: "❄️", From: "12-27", To: "02-29",
				Source: Source{Type: "rules", Rules: any_(
					r("summary", "contains", "Winter", "Schnee", "Snow", "Eis", "Ski", "Silvester", "New Year"),
					r("genre", "is", "Fantasy", "Familie", "Family", "Animation"),
				)},
			},
			{
				ID: "easter", Name: i18n.T(l, "season.easter"), Emoji: "🐣", From: "03-20", To: "04-25",
				Source: Source{Type: "rules", Rules: any_(
					r("title", "contains", "Oster", "Easter", "Hase", "Bunny", "Peter Hase"),
					r("summary", "contains", "Ostern", "Easter", "Osterhase"),
					r("genre", "is", "Familie", "Family", "Animation"),
				)},
			},
			{
				ID: "spring", Name: i18n.T(l, "season.spring"), Emoji: "🌷", From: "03-01", To: "05-31",
				Source: Source{Type: "rules", Rules: any_(
					r("genre", "is", "Liebesfilm", "Romance", "Romantik", "Komödie", "Comedy"),
					r("summary", "contains", "Frühling", "Spring", "Hochzeit", "Wedding"),
				)},
			},
			{
				ID: "summer", Name: i18n.T(l, "season.summer"), Emoji: "☀️", From: "06-01", To: "08-31",
				Source: Source{Type: "rules", Rules: any_(
					r("genre", "is", "Abenteuer", "Adventure", "Action"),
					r("summary", "contains", "Sommer", "Summer", "Strand", "Beach", "Urlaub", "Ferien", "Vacation", "Insel", "Island", "Meer"),
				)},
			},
			{
				ID: "halloween", Name: i18n.T(l, "season.halloween"), Emoji: "🎃", From: "10-15", To: "11-02",
				Source: Source{Type: "rules", Rules: any_(
					r("genre", "is", "Horror"),
					r("title", "contains", "Halloween"),
					r("summary", "contains", "Halloween", "Hexe", "Witch", "Geist", "Ghost", "Vampir", "Vampire", "Spuk"),
				)},
			},
			{
				ID: "autumn", Name: i18n.T(l, "season.autumn"), Emoji: "🍂", From: "09-01", To: "11-30",
				Source: Source{Type: "rules", Rules: any_(
					r("genre", "is", "Krimi", "Crime", "Mystery", "Thriller", "Drama"),
					r("summary", "contains", "Herbst", "Autumn", "Fall", "Nebel"),
				)},
			},
		},
		Fallback: &Source{Type: "rules", Rules: &rules.Group{Match: "all"}},
	}
	return c
}
