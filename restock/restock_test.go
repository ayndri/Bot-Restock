package restock

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in   string
		want Command
		err  error
	}{
		{"beli sunscreen 89rb", Command{Buy, "sunscreen", 89_000}, nil},
		{"Beli  Sabun Cuci MUKA  Rp45.000", Command{Buy, "sabun cuci muka", 45_000}, nil},
		{"beli 89rb sunscreen", Command{Buy, "sunscreen", 89_000}, nil},
		{"beli sunscreen", Command{Buy, "sunscreen", 0}, nil},
		{"beli sunscreen spf 50 89rb", Command{Buy, "sunscreen spf 50", 89_000}, nil},
		{"beli sunscreen spf 50", Command{Buy, "sunscreen spf 50", 0}, nil},
		{"beli shampo rp 32.500", Command{Buy, "shampo", 32_500}, nil},
		{"beli sikat gigi 15000", Command{Buy, "sikat gigi", 15_000}, nil},
		{"habis sunscreen", Command{Finish, "sunscreen", 0}, nil},
		{"abis Sabun Cuci Muka", Command{Finish, "sabun cuci muka", 0}, nil},
		// "habis" tidak membaca harga; angka ikut jadi nama.
		{"habis serum 20ml", Command{Finish, "serum 20ml", 0}, nil},
		{"beli 89rb", Command{Buy, "", 89_000}, ErrNoName},
		{"habis", Command{Finish, "", 0}, ErrNoName},
		{"udah potong rambut", Command{Done, "potong rambut", 0}, nil},
		{"Sudah Ganti Sprei", Command{Done, "ganti sprei", 0}, nil},
		{"udah beli sunscreen 89rb", Command{Buy, "sunscreen", 89_000}, nil},
		{"udah habis sunscreen", Command{Finish, "sunscreen", 0}, nil},
		{"udah", Command{Done, "", 0}, ErrNoName},
		{"kopi 25rb", Command{}, ErrUnknown},
		{"", Command{}, ErrUnknown},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if !errors.Is(err, tt.err) || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v; mau %+v, %v", tt.in, got, err, tt.want, tt.err)
		}
	}
}

func TestParseNameTooLong(t *testing.T) {
	long := "beli " + strings.Repeat("a", MaxNameLen+1)
	if _, err := Parse(long); !errors.Is(err, ErrNameTooLong) {
		t.Errorf("nama %d huruf: err = %v, mau ErrNameTooLong", MaxNameLen+1, err)
	}
}

func TestPredict(t *testing.T) {
	tests := []struct {
		name   string
		cycles []int
		guess  int
		want   int
		ok     bool
	}{
		{"tanpa riwayat dan tebakan", nil, 0, 0, false},
		{"tanpa riwayat, pakai tebakan", nil, 30, 30, true},
		{"riwayat mengalahkan tebakan", []int{40}, 30, 40, true},
		{"dua siklus dirata-rata", []int{40, 45}, 0, 43, true},
		{"median tiga terakhir", []int{100, 40, 38, 44}, 0, 40, true},
		// 90 hari = lupa lapor; dibuang karena lebih dari 2× median.
		{"lupa lapor dibuang", []int{40, 42, 90, 41}, 0, 41, true},
		// Satu siklus 5 hari (salah ketik "habis") juga dibuang.
		{"terlalu cepat dibuang", []int{40, 5, 42}, 0, 41, true},
		{"cuma lihat lima terakhir", []int{10, 10, 10, 40, 40, 40, 40, 40}, 0, 40, true},
	}
	for _, tt := range tests {
		got, ok := Predict(tt.cycles, tt.guess)
		if got != tt.want || ok != tt.ok {
			t.Errorf("%s: Predict(%v, %d) = %d, %v; mau %d, %v", tt.name, tt.cycles, tt.guess, got, ok, tt.want, tt.ok)
		}
	}
}

func day(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, WIB)
}

func TestCycleDays(t *testing.T) {
	tests := []struct {
		opened, finished time.Time
		want             int
	}{
		{day(2026, 10, 1, 8), day(2026, 10, 1, 21), 1},
		// Dibuka Senin malam, habis Selasa pagi: tetap 1 hari.
		{day(2026, 10, 5, 23), day(2026, 10, 6, 6), 1},
		{day(2026, 9, 1, 8), day(2026, 10, 11, 8), 40},
		// Jam 23.30 WIB masih tanggal yang sama walau di UTC sudah beda hari.
		{day(2026, 10, 1, 8), time.Date(2026, 10, 3, 16, 30, 0, 0, time.UTC), 2},
	}
	for _, tt := range tests {
		if got := CycleDays(tt.opened, tt.finished); got != tt.want {
			t.Errorf("CycleDays(%v, %v) = %d, mau %d", tt.opened, tt.finished, got, tt.want)
		}
	}
}

func TestNeedsReminder(t *testing.T) {
	opened := day(2026, 9, 1, 8)
	base := func() Item {
		return Item{Name: "sunscreen", GuessDays: 40, RemindBefore: 5, Active: &Unit{OpenedAt: opened}}
	}
	// Perkiraan habis 11 Okt, jadi pengingat mulai 6 Okt.
	tests := []struct {
		name  string
		item  func() Item
		today time.Time
		want  bool
	}{
		{"belum waktunya", base, day(2026, 10, 5, 0), false},
		{"tepat H-5", base, day(2026, 10, 6, 0), true},
		{"lewat perkiraan", base, day(2026, 10, 20, 0), true},
		{"masih ada cadangan", func() Item { it := base(); it.Spares = 1; return it }, day(2026, 10, 6, 0), false},
		{"sudah diingatkan", func() Item { it := base(); it.Active.RemindedAt = day(2026, 10, 6, 8); return it }, day(2026, 10, 7, 0), false},
		{"ditunda", func() Item { it := base(); it.Active.SnoozeUntil = day(2026, 10, 9, 0); return it }, day(2026, 10, 8, 0), false},
		{"tunda selesai", func() Item { it := base(); it.Active.SnoozeUntil = day(2026, 10, 9, 0); return it }, day(2026, 10, 9, 0), true},
		{"tanpa perkiraan", func() Item { it := base(); it.GuessDays = 0; return it }, day(2026, 10, 20, 0), false},
		{"tidak ada yang dipakai", func() Item { it := base(); it.Active = nil; return it }, day(2026, 10, 20, 0), false},
	}
	for _, tt := range tests {
		if got := tt.item().NeedsReminder(tt.today); got != tt.want {
			t.Errorf("%s: NeedsReminder = %v, mau %v", tt.name, got, tt.want)
		}
	}
}

func TestMatch(t *testing.T) {
	items := []Item{{ID: 1, Name: "sabun cuci muka"}, {ID: 2, Name: "sabun mandi"}, {ID: 3, Name: "sunscreen"}}
	tests := []struct {
		query string
		want  []int64
	}{
		{"Sunscreen", []int64{3}},
		{"sabun cuci muka", []int64{1}},
		{"cuci muka", []int64{1}},
		{"sabun", []int64{1, 2}},
		{"serum", nil},
	}
	for _, tt := range tests {
		var got []int64
		for _, it := range Match(items, tt.query) {
			got = append(got, it.ID)
		}
		if len(got) != len(tt.want) {
			t.Errorf("Match(%q) = %v, mau %v", tt.query, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("Match(%q) = %v, mau %v", tt.query, got, tt.want)
			}
		}
	}
}

func TestHabitDue(t *testing.T) {
	logs := func(days ...int) []HabitLog {
		var out []HabitLog
		for i, d := range days {
			out = append(out, HabitLog{ID: int64(i + 1), DoneAt: day(2026, 9, 1, 10).AddDate(0, 0, d)})
		}
		return out
	}
	tests := []struct {
		name  string
		h     Habit
		today time.Time
		want  bool
	}{
		{"tebakan, belum waktunya", Habit{GuessDays: 30, Logs: logs(0)}, day(2026, 9, 30, 0), false},
		{"tebakan, tepat waktunya", Habit{GuessDays: 30, Logs: logs(0)}, day(2026, 10, 1, 0), true},
		{"tanpa tebakan dan riwayat", Habit{Logs: logs(0)}, day(2027, 1, 1, 0), false},
		// Jarak 28 dan 32 hari → median 30, dihitung dari catatan terakhir (31 Okt).
		{"dari riwayat", Habit{GuessDays: 7, Logs: logs(0, 28, 60)}, day(2026, 11, 29, 0), false},
		{"dari riwayat, waktunya", Habit{GuessDays: 7, Logs: logs(0, 28, 60)}, day(2026, 11, 30, 0), true},
	}
	for _, tt := range tests {
		if got := tt.h.NeedsReminder(tt.today); got != tt.want {
			due, _ := tt.h.DueDate()
			t.Errorf("%s: NeedsReminder = %v, mau %v (perkiraan %v)", tt.name, got, tt.want, due)
		}
	}

	h := Habit{GuessDays: 30, Logs: logs(0)}
	h.Logs[0].RemindedAt = day(2026, 10, 1, 8)
	if h.NeedsReminder(day(2026, 10, 5, 0)) {
		t.Error("pengingat dikirim ulang")
	}
	h.Logs[0] = HabitLog{DoneAt: h.Logs[0].DoneAt, SnoozeUntil: day(2026, 10, 8, 0)}
	if h.NeedsReminder(day(2026, 10, 7, 0)) || !h.NeedsReminder(day(2026, 10, 8, 0)) {
		t.Error("masa tunda tidak dihormati")
	}
}
