package tui

import (
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Much of what the TUI draws comes from a computer: file names and contents, diffs, an agent's
// answers and its tools' output, a browser's address. None of it may talk to the terminal the TUI
// runs in, so every answer Studio gives is cleaned as it is decoded: escape sequences go, and the
// other control characters show as their pictures (␛, ␇…). Tabs and new lines stay.

// cleanText is s with its escape sequences removed, a carriage return as a new line, other
// control characters as their pictures, and invalid UTF-8 as �.
func cleanText(s string) string {
	if !needsCleaning(s) {
		return s
	}
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = ansi.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			b.WriteByte('\n')
		case r < 0x20:
			b.WriteRune(0x2400 + r) // ␀ … ␟
		case r == 0x7f:
			b.WriteRune('␡')
		case r >= 0x80 && r < 0xa0:
			b.WriteRune('�') // C1 controls, which some terminals obey
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// needsCleaning reports whether s has anything cleanText changes.
func needsCleaning(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\n' || c == '\t':
		case c < 0x20 || c == 0x7f:
			return true
		case c == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] < 0xa0:
			return true // the UTF-8 of a C1 control
		case c >= 0x80:
			if !utf8.ValidString(s[i:]) {
				return true
			}
			// The rest is valid UTF-8: only a C1 control is left to find.
			return strings.ContainsFunc(s[i:], func(r rune) bool { return r < 0x20 && r != '\n' && r != '\t' || r >= 0x7f && r < 0xa0 })
		}
	}
	return false
}

// scrub cleans every string v points to, as cleanText does: the fields of structs, the
// elements of slices and arrays, the keys and values of maps. Bytes, such as a file's data or
// raw JSON, are left as they are.
func scrub(v any) {
	scrubValue(reflect.ValueOf(v))
}

func scrubValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Interface {
			inner := v.Elem()
			if !v.CanSet() {
				scrubValue(inner)
				return
			}
			c := reflect.New(inner.Type()).Elem()
			c.Set(inner)
			scrubValue(c)
			v.Set(c)
			return
		}
		scrubValue(v.Elem())
	case reflect.String:
		if v.CanSet() {
			if s := v.String(); needsCleaning(s) {
				v.SetString(cleanText(s))
			}
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if f := v.Field(i); f.CanSet() {
				scrubValue(f)
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := range v.Len() {
			scrubValue(v.Index(i))
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		et := v.Type().Elem()
		for _, k := range v.MapKeys() {
			e := reflect.New(et).Elem()
			e.Set(v.MapIndex(k))
			scrubValue(e)
			nk := k
			if k.Kind() == reflect.String && needsCleaning(k.String()) {
				v.SetMapIndex(k, reflect.Value{})
				nk = reflect.ValueOf(cleanText(k.String())).Convert(k.Type())
			}
			v.SetMapIndex(nk, e)
		}
	}
}
