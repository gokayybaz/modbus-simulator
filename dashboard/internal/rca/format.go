package rca

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

func fmtF(v float64, prec int) string { return strconv.FormatFloat(v, 'f', prec, 64) }

func fmtSigned(v float64, prec int) string {
	s := fmtF(v, prec)
	if v >= 0 {
		return "+" + s
	}
	return s
}

func itoa(i int) string { return strconv.Itoa(i) }

func lower(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return strings.ToLower(string(r)) + s[n:]
}
