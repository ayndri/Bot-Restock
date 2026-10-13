package restock

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseExpiry(t *testing.T) {
	today := time.Date(2026, 10, 13, 0, 0, 0, 0, WIB)
	d := func(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 0, 0, 0, 0, WIB) }

	tests := []struct {
		in   string
		want time.Time
	}{
		{"15/8/2027", d(2027, 8, 15)},
		{"15-08-27", d(2027, 8, 15)},
		{"15.8.2027", d(2027, 8, 15)},
		{"8/2027", d(2027, 8, 31)},
		{"02/2028", d(2028, 2, 29)}, // kabisat
		{"08/27", d(2027, 8, 31)},   // 27 bukan bulan → bulan/tahun
		{"agu 2027", d(2027, 8, 31)},
		{"Agustus 27", d(2027, 8, 31)},
		{"20/12", d(2026, 12, 20)}, // tahun ini
		{"1/3", d(2027, 3, 1)},     // sudah lewat tahun ini → tahun depan
		{"13/10", d(2026, 10, 13)}, // hari ini masih boleh
	}
	for _, tt := range tests {
		got, n, err := ParseExpiry(splitWords(tt.in), today)
		if err != nil || n == 0 || !got.Equal(tt.want) {
			t.Errorf("ParseExpiry(%q) = %v, %d, %v; ingin %v", tt.in, got, n, err, tt.want)
		}
	}

	for _, in := range []string{"sunscreen", "31/2/2027", "13/2027", "agu", ""} {
		if _, n, err := ParseExpiry(splitWords(in), today); n != 0 || err != nil {
			t.Errorf("ParseExpiry(%q) seharusnya bukan tanggal: n=%d, err=%v", in, n, err)
		}
	}
	for _, in := range []string{"1/2026", "12/10/2026", "sep 2026"} {
		if _, _, err := ParseExpiry(splitWords(in), today); !errors.Is(err, ErrExpired) {
			t.Errorf("ParseExpiry(%q): err %v, ingin ErrExpired", in, err)
		}
	}
}

func TestSplitExpiry(t *testing.T) {
	today := time.Date(2026, 10, 13, 0, 0, 0, 0, WIB)
	tests := []struct {
		in, rest string
		want     time.Time
	}{
		{"beli sunscreen 89rb exp 8/2027", "beli sunscreen 89rb", time.Date(2027, 8, 31, 0, 0, 0, 0, WIB)},
		{"beli sunscreen EXP: agu 2027 89rb", "beli sunscreen 89rb", time.Date(2027, 8, 31, 0, 0, 0, 0, WIB)},
		{"kemarin beli toner kadaluwarsa 15/8/27", "kemarin beli toner", time.Date(2027, 8, 15, 0, 0, 0, 0, WIB)},
		{"beli sunscreen 89rb", "beli sunscreen 89rb", time.Time{}},
	}
	for _, tt := range tests {
		got, rest, err := SplitExpiry(tt.in, today)
		if err != nil || rest != tt.rest || !got.Equal(tt.want) {
			t.Errorf("SplitExpiry(%q) = %v, %q, %v; ingin %v, %q", tt.in, got, rest, err, tt.want, tt.rest)
		}
	}
	if _, _, err := SplitExpiry("beli serum exp", today); !errors.Is(err, ErrBadExpiry) {
		t.Errorf("kata kunci tanpa tanggal: err %v, ingin ErrBadExpiry", err)
	}
}

func TestExpiryOnly(t *testing.T) {
	today := time.Date(2026, 10, 13, 0, 0, 0, 0, WIB)
	for _, in := range []string{"8/2027", "exp 8/2027", "agu 2027"} {
		if _, ok, err := ExpiryOnly(in, today); !ok || err != nil {
			t.Errorf("ExpiryOnly(%q) = %v, %v; ingin tanggal", in, ok, err)
		}
	}
	for _, in := range []string{"beli sabun", "8/2027 beli sabun", "kemarin"} {
		if _, ok, _ := ExpiryOnly(in, today); ok {
			t.Errorf("ExpiryOnly(%q) seharusnya bukan tanggal saja", in)
		}
	}
}

func splitWords(s string) []string { return strings.Fields(s) }
