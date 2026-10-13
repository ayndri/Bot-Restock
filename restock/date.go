package restock

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Disalin dari Dompetku (ledger/date.go), ditambah dukungan tanggal di akhir.

var ErrFutureDate = errors.New("tanggalnya belum lewat")

var weekdays = map[string]time.Weekday{
	"minggu": time.Sunday,
	"senin":  time.Monday,
	"selasa": time.Tuesday,
	"rabu":   time.Wednesday,
	"kamis":  time.Thursday,
	"jumat":  time.Friday,
	"sabtu":  time.Saturday,
}

var yesterdayWords = []string{"kemarin", "kemaren", "kmrn", "kmarin"}

var dayMonthRe = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})$`)

// SplitDate memisahkan penanda tanggal di awal atau akhir teks: "kemarin",
// "kemarin lusa", nama hari ("senin" = Senin terakhir, termasuk hari ini),
// atau "5/10" (tanggal/bulan tahun ini). Jadi "kemarin beli sabun" dan
// "beli sabun kemarin" sama artinya.
//
// today adalah tengah malam hari ini di WIB. Kalau tidak ada penanda tanggal,
// day bernilai nol dan rest sama dengan text.
func SplitDate(text string, today time.Time) (day time.Time, rest string, err error) {
	fields := strings.Fields(text)
	if day, n, err := leadingDate(fields, today); err != nil || n > 0 {
		return day, strings.Join(fields[n:], " "), err
	}
	// Di akhir: coba dua kata dulu supaya "kemarin lusa" tidak terbaca "lusa".
	for _, k := range []int{2, 1} {
		if len(fields) <= k {
			continue
		}
		tail := fields[len(fields)-k:]
		day, n, err := leadingDate(tail, today)
		if err != nil {
			return time.Time{}, text, err
		}
		if n == k {
			return day, strings.Join(fields[:len(fields)-k], " "), nil
		}
	}
	return time.Time{}, text, nil
}

// leadingDate membaca penanda tanggal di awal fields dan mengembalikan
// jumlah kata yang terpakai (0 kalau tidak ada).
func leadingDate(fields []string, today time.Time) (time.Time, int, error) {
	if len(fields) == 0 {
		return time.Time{}, 0, nil
	}
	first := strings.ToLower(fields[0])

	if slices.Contains(yesterdayWords, first) {
		if len(fields) > 1 && strings.EqualFold(fields[1], "lusa") {
			return today.AddDate(0, 0, -2), 2, nil
		}
		return today.AddDate(0, 0, -1), 1, nil
	}
	if wd, ok := weekdays[first]; ok {
		back := (int(today.Weekday()) - int(wd) + 7) % 7
		return today.AddDate(0, 0, -back), 1, nil
	}
	if m := dayMonthRe.FindStringSubmatch(first); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		day := time.Date(today.Year(), time.Month(mo), d, 0, 0, 0, 0, today.Location())
		// time.Date menormalkan 31/2 jadi 3/3; anggap saja bukan tanggal.
		if day.Day() != d || int(day.Month()) != mo {
			return time.Time{}, 0, nil
		}
		if day.After(today) {
			return time.Time{}, 0, ErrFutureDate
		}
		return day, 1, nil
	}
	return time.Time{}, 0, nil
}
