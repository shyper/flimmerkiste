// Package i18n holds the texts Flimmerkiste shows to people – on screen, in the
// guide and in the web interface's server messages – in German and English,
// and formats times and dates the way each language writes them.
//
// One language applies to a whole installation. English is the fallback.
package i18n

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Lang is a supported language.
type Lang string

const (
	EN Lang = "en"
	DE Lang = "de"
)

// Supported lists the languages in the order offered to people.
var Supported = []Lang{EN, DE}

// Parse returns the language of a code such as "de", "de-AT" or "deu"
// (English for anything else).
func Parse(code string) Lang {
	code = strings.ToLower(strings.TrimSpace(code))
	switch {
	case code == "de" || code == "deu" || code == "ger" || strings.HasPrefix(code, "de-") || strings.HasPrefix(code, "de_"):
		return DE
	}
	return EN
}

// Known reports whether code names a supported language exactly ("de", "en").
func Known(code string) bool {
	for _, l := range Supported {
		if string(l) == code {
			return true
		}
	}
	return false
}

// FromAcceptLanguage picks the first supported language of an
// Accept-Language header (English without one).
func FromAcceptLanguage(header string) Lang {
	for _, part := range strings.Split(header, ",") {
		tag := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if tag == "" || tag == "*" {
			continue
		}
		base := strings.ToLower(strings.SplitN(strings.SplitN(tag, "-", 2)[0], "_", 2)[0])
		if base == "de" || base == "en" {
			return Parse(base)
		}
	}
	return EN
}

var catalogs = map[Lang]map[string]string{EN: en, DE: de}

// T returns the text of key in l, formatted with args like fmt.Sprintf. Error
// arguments are translated too. A key missing in l falls back to English,
// an unknown key is returned as is.
func T(l Lang, key string, args ...any) string {
	f, ok := catalogs[l][key]
	if !ok {
		if f, ok = en[key]; !ok {
			f = key
		}
	}
	if len(args) == 0 {
		return f
	}
	for i, a := range args {
		if err, ok := a.(error); ok {
			args[i] = Message(l, err)
		}
	}
	return fmt.Sprintf(f, args...)
}

// Text is a message to be shown later in whatever language applies then.
type Text struct {
	Key  string
	Args []any
}

// M creates a Text.
func M(key string, args ...any) Text { return Text{Key: key, Args: args} }

// In returns the text in l. Text arguments are translated too.
func (t Text) In(l Lang) string {
	args := make([]any, len(t.Args))
	for i, a := range t.Args {
		if tt, ok := a.(Text); ok {
			a = tt.In(l)
		}
		args[i] = a
	}
	return T(l, t.Key, args...)
}

// Error is an error whose message people see. Error() gives the English text
// (logs are English); Message the text in a language.
type Error struct {
	Text
}

// E creates an Error. An error among args is its cause (errors.Is/As see it).
func E(key string, args ...any) *Error { return &Error{Text{Key: key, Args: args}} }

func (e *Error) Error() string { return e.Message(EN) }

// Message is the text of the error in l.
func (e *Error) Message(l Lang) string { return e.In(l) }

func (e *Error) Unwrap() error {
	for _, a := range e.Args {
		if err, ok := a.(error); ok {
			return err
		}
	}
	return nil
}

// Message returns the text of err in l: translated where err (or the first
// error it wraps that carries a text) is an Error.
func Message(l Lang, err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		msg := e.Message(l)
		// keep context added around the Error (fmt.Errorf("x: %w", e))
		if outer := err.Error(); outer != e.Error() && strings.HasSuffix(outer, e.Error()) {
			return strings.TrimSuffix(outer, e.Error()) + msg
		}
		return msg
	}
	return err.Error()
}

// Clock writes a time of day: "20:15" or "8:15 PM".
func Clock(l Lang, t time.Time) string {
	if l == DE {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

// Weekday names a day.
func Weekday(l Lang, d time.Weekday) string {
	return T(l, "weekday."+d.String())
}

// Month names a month.
func Month(l Lang, m time.Month) string {
	return T(l, "month."+m.String())
}

// Date writes a day of the year: "16. Dezember" or "December 16", with the
// year if withYear ("7. Januar 2027", "January 7, 2027").
func Date(l Lang, t time.Time, withYear bool) string {
	if l == DE {
		s := fmt.Sprintf("%d. %s", t.Day(), Month(l, t.Month()))
		if withYear {
			s += fmt.Sprintf(" %d", t.Year())
		}
		return s
	}
	s := fmt.Sprintf("%s %d", Month(l, t.Month()), t.Day())
	if withYear {
		s += fmt.Sprintf(", %d", t.Year())
	}
	return s
}

// ShortDateTime writes a date and time briefly: "24.10. 18:00" or "10/24 6:00 PM".
func ShortDateTime(l Lang, t time.Time) string {
	if l == DE {
		return t.Format("02.01. 15:04")
	}
	return t.Format("1/2 3:04 PM")
}
