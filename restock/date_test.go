package restock

import (
	"errors"
	"testing"
	"time"
)

func TestSplitDate(t *testing.T) {
	today := time.Date(2026, 10, 7, 0, 0, 0, 0, WIB) // Rabu
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, WIB) }

	tests := []struct {
		in       string
		wantDay  time.Time
		wantRest string
	}{
		{"kemarin beli sunscreen 89rb", day(6), "beli sunscreen 89rb"},
		{"beli sunscreen 89rb kemarin", day(6), "beli sunscreen 89rb"},
		{"Kmrn habis serum", day(6), "habis serum"},
		{"kemarin lusa beli sabun", day(5), "beli sabun"},
		{"beli sabun kemarin lusa", day(5), "beli sabun"},
		{"senin beli shampo", day(5), "beli shampo"},
		{"habis shampo senin", day(5), "habis shampo"},
		{"rabu beli sabun", day(7), "beli sabun"}, // hari ini sendiri
		{"kamis beli sabun", day(1), "beli sabun"},
		{"5/10 beli sabun 20rb", day(5), "beli sabun 20rb"},
		{"beli sabun 20rb 5/10", day(5), "beli sabun 20rb"},
		{"kemarin", day(6), ""},
		{"beli sunscreen spf 50", time.Time{}, "beli sunscreen spf 50"},
		{"beli 31/2 sabun", time.Time{}, "beli 31/2 sabun"}, // bukan tanggal
	}
	for _, tt := range tests {
		got, rest, err := SplitDate(tt.in, today)
		if err != nil {
			t.Errorf("SplitDate(%q) error: %v", tt.in, err)
			continue
		}
		if !got.Equal(tt.wantDay) || rest != tt.wantRest {
			t.Errorf("SplitDate(%q) = %v, %q; ingin %v, %q", tt.in, got, rest, tt.wantDay, tt.wantRest)
		}
	}

	for _, in := range []string{"20/10 beli sabun", "beli sabun 20/10"} {
		if _, _, err := SplitDate(in, today); !errors.Is(err, ErrFutureDate) {
			t.Errorf("SplitDate(%q): err %v, ingin ErrFutureDate", in, err)
		}
	}
}
