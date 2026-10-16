package logo

import "testing"

func BenchmarkStyles(b *testing.B) {
	for _, s := range Styles {
		b.Run(s.ID, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := Render(Options{Name: "Jahreszeiten-Kanal", Style: s.ID}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
