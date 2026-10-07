package restock

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Action string

const (
	Buy    Action = "buy"
	Finish Action = "finish"
)

// Command adalah hasil membaca satu pesan, misalnya "beli sunscreen 89rb".
type Command struct {
	Action Action
	Name   string // sudah dinormalisasi
	Price  int64  // 0 = tidak disebut
}

// MaxNameLen membatasi panjang nama barang (dalam huruf).
const MaxNameLen = 60

var (
	ErrUnknown     = errors.New("bukan perintah beli/habis")
	ErrNoName      = errors.New("nama barangnya kosong")
	ErrNameTooLong = errors.New("nama barangnya kepanjangan")
)

var verbs = map[string]Action{
	"beli":  Buy,
	"habis": Finish,
	"abis":  Finish,
}

// Parse membaca "beli <nama> [harga]" atau "habis <nama>".
//
// Harga boleh di mana saja: "beli 89rb sunscreen" juga bisa. Angka polos di
// bawah seribu dianggap bagian dari nama ("sunscreen spf 50"), bukan harga.
func Parse(text string) (Command, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Command{}, ErrUnknown
	}
	action, ok := verbs[strings.ToLower(fields[0])]
	if !ok {
		return Command{}, ErrUnknown
	}
	fields = fields[1:]

	cmd := Command{Action: action}
	if action == Buy {
		// Kalau ada beberapa angka, yang bersatuan menang. Kalau sama-sama
		// bersatuan atau sama-sama polos, yang terakhir menang.
		best, bestScaled := -1, false
		for i, f := range fields {
			v, scaled, ok := parseAmount(f)
			if !ok || (bestScaled && !scaled) || (!scaled && v < 1000) {
				continue
			}
			best, bestScaled, cmd.Price = i, scaled, v
		}
		if best >= 0 {
			fields = slices.Delete(fields, best, best+1)
		}
		fields = slices.DeleteFunc(fields, func(f string) bool {
			return strings.EqualFold(f, "rp") || strings.EqualFold(f, "rp.")
		})
	}

	cmd.Name = Normalize(strings.Join(fields, " "))
	switch {
	case cmd.Name == "":
		return cmd, ErrNoName
	case utf8.RuneCountInString(cmd.Name) > MaxNameLen:
		return cmd, ErrNameTooLong
	}
	return cmd, nil
}

// Normalize membuat "  Sabun  Cuci MUKA " menjadi "sabun cuci muka".
func Normalize(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// Display membuat "sabun cuci muka" menjadi "Sabun cuci muka".
func Display(name string) string {
	r, size := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return name
	}
	return string(unicode.ToUpper(r)) + name[size:]
}

// Match mencari barang untuk "habis <nama>". Nama yang persis sama menang.
// Kalau tidak ada, barang yang namanya memuat kata yang diketik (atau
// sebaliknya) ikut dikembalikan; pemanggil yang memutuskan kalau hasilnya
// lebih dari satu.
func Match(items []Item, query string) []Item {
	query = Normalize(query)
	var partial []Item
	for _, it := range items {
		if it.Name == query {
			return []Item{it}
		}
		if strings.Contains(it.Name, query) || strings.Contains(query, it.Name) {
			partial = append(partial, it)
		}
	}
	return partial
}
