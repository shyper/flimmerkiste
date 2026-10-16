package stream

import (
	"fmt"
	"strings"
	"testing"
)

// The clock must not jump between a slate and the credits panel.
func TestClockSameOnSlatesAndCredits(t *testing.T) {
	for _, h := range []int{720, 1080} {
		w := h * 16 / 9
		v := CreditsVideo(w, h)
		right, top := CardClockMargin(h)
		credits := buildCreditsArgs("in.mkv", 0, 0, 0, 0, 0, 1, 0, "list.txt", "clock.txt", Options{NormalizeRes: h})
		want := fmt.Sprintf("overlay=x=W-w-%d:y=%d", right, top)
		if !strings.Contains(strings.Join(credits, " "), want) || right != v.Min.X-ClockPad(h) {
			t.Errorf("%dp: slate clock %d/%d, credits %v", h, right, top, credits)
		}
	}
}
