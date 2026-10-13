package restock

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ExpiryRemindBefore adalah berapa hari sebelum kadaluwarsa bot mengingatkan.
const ExpiryRemindBefore = 14

var (
	ErrExpired    = errors.New("tanggal kadaluwarsanya udah lewat")
	ErrBadExpiry  = errors.New("tanggal kadaluwarsanya nggak kebaca")
	expiryWords   = []string{"exp", "ed", "expired", "kadaluwarsa", "kedaluwarsa", "kadaluarsa", "kedaluarsa"}
	dayMonthYear  = regexp.MustCompile(`^(\d{1,2})[/.-](\d{1,2})[/.-](\d{2}|\d{4})$`)
	monthLongYear = regexp.MustCompile(`^(\d{1,2})[/.-](\d{4})$`)
	numberPair    = regexp.MustCompile(`^(\d{1,2})[/.-](\d{1,2})$`)
	yearOnly      = regexp.MustCompile(`^(\d{2}|\d{4})$`)
)

var monthWords = map[string]time.Month{
	"jan": 1, "januari": 1,
	"feb": 2, "februari": 2,
	"mar": 3, "maret": 3,
	"apr": 4, "april": 4,
	"mei": 5, "may": 5,
	"jun": 6, "juni": 6,
	"jul": 7, "juli": 7,
	"agu": 8, "agt": 8, "agustus": 8, "aug": 8,
	"sep": 9, "september": 9,
	"okt": 10, "oktober": 10, "oct": 10,
	"nov": 11, "november": 11,
	"des": 12, "desember": 12, "dec": 12,
}

func isExpiryWord(s string) bool {
	return slices.Contains(expiryWords, strings.TrimSuffix(strings.ToLower(s), ":"))
}

// ParseExpiry membaca tanggal kadaluwarsa di awal fields, dengan format yang
// biasa tercetak di kemasan:
//
//	15/8/2027, 15-08-27   tanggal lengkap
//	8/2027, 08/27         bulan dan tahun → akhir bulan itu
//	agu 2027, agustus 27  sama, nama bulan
//	15/8                  tanggal dan bulan, tahun ini (tahun depan kalau sudah lewat)
//
// "08/27" dibaca bulan/tahun karena 27 bukan bulan. n adalah jumlah kata yang
// terpakai, 0 kalau fields tidak diawali tanggal.
func ParseExpiry(fields []string, today time.Time) (day time.Time, n int, err error) {
	if len(fields) == 0 {
		return time.Time{}, 0, nil
	}
	first := strings.ToLower(fields[0])
	num := func(s string) int { v, _ := strconv.Atoi(s); return v }
	year := func(s string) int {
		if len(s) == 2 {
			return 2000 + num(s)
		}
		return num(s)
	}

	n = 1
	if m := dayMonthYear.FindStringSubmatch(first); m != nil {
		day = date(year(m[3]), num(m[2]), num(m[1]))
	} else if m := monthLongYear.FindStringSubmatch(first); m != nil {
		day = endOfMonth(num(m[2]), num(m[1]))
	} else if m := numberPair.FindStringSubmatch(first); m != nil {
		a, b := num(m[1]), num(m[2])
		if b > 12 {
			day = endOfMonth(2000+b, a)
		} else if day = date(today.Year(), b, a); !day.IsZero() && day.Before(today) {
			day = date(today.Year()+1, b, a)
		}
	} else if mo, ok := monthWords[first]; ok && len(fields) > 1 && yearOnly.MatchString(fields[1]) {
		day, n = endOfMonth(year(fields[1]), int(mo)), 2
	} else {
		return time.Time{}, 0, nil
	}

	if day.IsZero() {
		return time.Time{}, 0, nil
	}
	if day.Before(today) {
		return time.Time{}, n, ErrExpired
	}
	return day, n, nil
}

// SplitExpiry memisahkan "exp 8/2027" (atau "kadaluwarsa 8/2027", "ed 08/27")
// dari pesan, di posisi mana pun. Kalau tidak ada kata kunci, day nol dan
// rest sama dengan text.
func SplitExpiry(text string, today time.Time) (day time.Time, rest string, err error) {
	fields := strings.Fields(text)
	i := slices.IndexFunc(fields, isExpiryWord)
	if i < 0 {
		return time.Time{}, text, nil
	}
	day, n, err := ParseExpiry(fields[i+1:], today)
	if err != nil {
		return time.Time{}, text, err
	}
	if n == 0 {
		return time.Time{}, text, ErrBadExpiry
	}
	return day, strings.Join(slices.Delete(fields, i, i+1+n), " "), nil
}

// ExpiryOnly membaca pesan yang isinya cuma tanggal kadaluwarsa, boleh
// diawali kata kunci: "8/2027" atau "exp 8/2027". ok=false kalau pesannya
// bukan tanggal sama sekali.
func ExpiryOnly(text string, today time.Time) (day time.Time, ok bool, err error) {
	fields := strings.Fields(text)
	if len(fields) > 0 && isExpiryWord(fields[0]) {
		fields = fields[1:]
	}
	day, n, err := ParseExpiry(fields, today)
	if n == 0 || n != len(fields) {
		return time.Time{}, false, nil
	}
	return day, true, err
}

// date mengembalikan nol kalau tanggalnya tidak ada (31/2, bulan 13).
func date(y, m, d int) time.Time {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, WIB)
	if t.Day() != d || int(t.Month()) != m {
		return time.Time{}
	}
	return t
}

func endOfMonth(y, m int) time.Time {
	if m < 1 || m > 12 {
		return time.Time{}
	}
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, WIB)
}
