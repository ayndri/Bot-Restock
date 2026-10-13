package bot

import (
	"cmp"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"

	"github.com/ayndri/restok/restock"
)

const helpText = `👋 <b>Halo, aku Restok.</b>
Aku nyatet barang pribadimu, belajar seberapa cepat kamu ngabisinnya, terus ngingetin sebelum habis.

<b>Waktu beli</b>
<code>beli sunscreen 89rb</code>
<code>beli sabun cuci muka</code> (harga boleh nggak ditulis)

<b>Waktu habis</b>
<code>habis sunscreen</code>

<b>Lupa nyatet?</b> Tambahin tanggalnya di depan atau di belakang
<code>kemarin beli sunscreen 89rb</code>
<code>habis serum senin</code>
<code>5/10 beli shampo</code> (tanggal/bulan)

<b>Kegiatan rutin</b>
<code>udah potong rambut</code>
<code>kemarin udah ganti sprei</code>
Aku pelajari jaraknya, terus ngingetin waktu udah waktunya lagi.

<b>Kadaluwarsa</b> (boleh nggak diisi)
<code>beli sunscreen 89rb exp 8/2027</code>
atau tekan tombol 📅 di bawah catatan beli. Aku ingetin 14 hari sebelumnya.

Makin sering kamu catat, makin pas perkiraannya. Kalau beli lagi padahal yang lama belum habis, yang baru disimpan sebagai cadangan.

<b>Perintah</b>
/stok — semua barang dan perkiraan habisnya
/rutin — kegiatan rutin dan kapan waktunya lagi
/batal — batalkan catatan terakhir

Tiap jam 8 pagi aku ngabarin barang yang hampir habis dan kegiatan yang udah waktunya.`

const parseHint = `Aku baru ngerti tiga hal 🙂
<code>beli sunscreen 89rb</code> waktu beli
<code>habis sunscreen</code> waktu habis
<code>udah potong rambut</code> buat kegiatan rutin

Ketik /bantuan buat lihat semuanya.`

var monthNames = [...]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// shortDate memformat tanggal menjadi "11 Okt".
func shortDate(t time.Time) string {
	t = t.In(restock.WIB)
	return fmt.Sprintf("%d %s", t.Day(), monthNames[t.Month()-1])
}

// longDate memformat tanggal menjadi "31 Agu 2027".
func longDate(t time.Time) string {
	return fmt.Sprintf("%s %d", shortDate(t), t.In(restock.WIB).Year())
}

// relDay memformat jarak hari: "hari ini", "besok", "6 hari lagi", "lewat 3 hari".
func relDay(due, today time.Time) string {
	switch n := restock.DaysBetween(today, due); {
	case n == 0:
		return "hari ini"
	case n == 1:
		return "besok"
	case n > 1:
		return fmt.Sprintf("%d hari lagi", n)
	default:
		return fmt.Sprintf("lewat %d hari", -n)
	}
}

func bold(name string) string {
	return "<b>" + html.EscapeString(restock.Display(name)) + "</b>"
}

func formatBought(it restock.Item, price int64, spare bool) string {
	var s strings.Builder
	fmt.Fprintf(&s, "🛒 %s dicatat", bold(it.Name))
	if price > 0 {
		s.WriteString(" · " + restock.Rupiah(price))
	}
	if spare {
		fmt.Fprintf(&s, "\n📦 Disimpan sebagai cadangan, yang lama masih dipakai. Total cadangan: %d.", it.Spares)
	} else if due, ok := it.DueDate(); ok {
		days, _ := it.Cycle()
		fmt.Fprintf(&s, "\n⏳ Perkiraan habis <b>%s</b> (±%d hari)", shortDate(due), days)
	}
	return s.String()
}

func formatGuessSet(it restock.Item, days int) string {
	if days == 0 {
		return fmt.Sprintf("🛒 %s dicatat\n\nOke, nanti aku pelajari sendiri setelah barang ini habis sekali. Jangan lupa ketik <code>habis %s</code> ya.",
			bold(it.Name), html.EscapeString(it.Name))
	}
	s := fmt.Sprintf("🛒 %s dicatat\n\nOke, aku anggap habis dalam ±%d hari.", bold(it.Name), days)
	if due, ok := it.DueDate(); ok {
		s += fmt.Sprintf(" Perkiraan habis <b>%s</b>.", shortDate(due))
	}
	return s + "\nSetelah habis beneran, perkiraannya aku sesuaikan sendiri."
}

// dateLine kosong untuk catatan hari ini, atau "\n📅 Dicatat untuk tanggal ...".
func formatFinished(it restock.Item, res restock.FinishResult, dateLine string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "✅ %s habis setelah <b>%d hari</b>.%s", bold(it.Name), res.Days, dateLine)
	if len(it.Cycles) >= 2 {
		if days, ok := it.Cycle(); ok {
			fmt.Fprintf(&s, "\nPerkiraan pemakaianmu sekarang ±%d hari.", days)
		}
	}

	if res.OpenedSpare {
		s.WriteString("\n\n📦 Cadangan mulai dipakai.")
		if due, ok := it.DueDate(); ok {
			fmt.Fprintf(&s, " Perkiraan habis <b>%s</b>.", shortDate(due))
		}
		if it.Spares > 0 {
			fmt.Fprintf(&s, " Masih ada %d cadangan lagi.", it.Spares)
		}
	} else {
		fmt.Fprintf(&s, "\n\n🛒 Nggak ada cadangan. Kalau udah beli lagi, ketik <code>beli %s</code>.", html.EscapeString(it.Name))
	}
	s.WriteString("\n\nSalah? /batal")
	return s.String()
}

func formatExpirySet(it restock.Item, unitID int64, day time.Time) string {
	s := fmt.Sprintf("📅 Kadaluwarsa %s: <b>%s</b>.\nAku ingetin %d hari sebelumnya.",
		bold(it.Name), longDate(day), restock.ExpiryRemindBefore)
	if it.Active != nil && it.Active.ID == unitID {
		if due, ok := it.DueDate(); ok && day.Before(due) {
			s += fmt.Sprintf("\n\n⚠️ Ini lebih cepat dari perkiraan habisnya (%s). Kayaknya bakal kadaluwarsa sebelum habis.", shortDate(due))
		}
	}
	return s
}

// formatExpiryReminder dipakai untuk unit yang sedang dipakai maupun cadangan.
func formatExpiryReminder(it restock.Item, u restock.Unit, today time.Time) string {
	var when string
	switch n := restock.DaysBetween(today, u.ExpiresOn); {
	case n > 1:
		when = fmt.Sprintf("kadaluwarsa <b>%d hari lagi</b> (%s)", n, longDate(u.ExpiresOn))
	case n == 1:
		when = "kadaluwarsa <b>besok</b>"
	case n == 0:
		when = "kadaluwarsa <b>hari ini</b>"
	default:
		when = fmt.Sprintf("udah kadaluwarsa sejak <b>%s</b>", longDate(u.ExpiresOn))
	}
	if it.Active != nil && it.Active.ID == u.ID {
		return fmt.Sprintf("⏰ %s %s.\n\nHabisin duluan ya. Kalau udah dibuang, ketik <code>habis %s</code>.",
			bold(it.Name), when, html.EscapeString(it.Name))
	}
	return fmt.Sprintf("⏰ %s (yang cadangan, belum dibuka) %s.\n\nCadangan yang paling cepat kadaluwarsa aku buka duluan waktu yang lama habis.",
		bold(it.Name), when)
}

func formatReminder(it restock.Item, today time.Time) string {
	due, _ := it.DueDate()
	var when string
	switch n := restock.DaysBetween(today, due); {
	case n > 1:
		when = fmt.Sprintf("kemungkinan habis sekitar <b>%d hari lagi</b> (%s)", n, shortDate(due))
	case n == 1:
		when = "kemungkinan habis <b>besok</b>"
	case n == 0:
		when = "kemungkinan habis <b>hari ini</b>"
	default:
		when = fmt.Sprintf("menurut perkiraan harusnya udah habis sejak <b>%s</b>", shortDate(due))
	}
	return fmt.Sprintf("🔔 %s %s.\n\nUdah beli lagi?", bold(it.Name), when)
}

// formatStock mengurutkan barang dari yang paling cepat habis. Barang tanpa
// perkiraan menyusul, lalu barang yang sudah habis dan belum dibeli lagi.
func formatStock(items []restock.Item, today time.Time) string {
	if len(items) == 0 {
		return "Belum ada barang. Coba kirim <code>beli sunscreen 89rb</code>."
	}

	type row struct {
		it    restock.Item
		due   time.Time
		group int // 0 ada perkiraan, 1 dipakai tanpa perkiraan, 2 tidak dipakai
	}
	rows := make([]row, 0, len(items))
	for _, it := range items {
		r := row{it: it, group: 2}
		if it.Active != nil {
			r.group = 1
			if due, ok := it.DueDate(); ok {
				r.due, r.group = due, 0
			}
		}
		rows = append(rows, r)
	}
	slices.SortStableFunc(rows, func(a, b row) int {
		return cmp.Or(cmp.Compare(a.group, b.group), a.due.Compare(b.due))
	})

	var s strings.Builder
	s.WriteString("<b>🧴 Stok kamu</b>\n")
	for _, r := range rows {
		fmt.Fprintf(&s, "\n• %s — ", bold(r.it.Name))
		switch r.group {
		case 0:
			fmt.Fprintf(&s, "habis ±%s (%s)", shortDate(r.due), relDay(r.due, today))
		case 1:
			fmt.Fprintf(&s, "dipakai sejak %s, belum ada perkiraan", shortDate(r.it.Active.OpenedAt))
		default:
			if r.it.Spares > 0 {
				s.WriteString("belum dibuka")
			} else {
				s.WriteString("habis, belum beli lagi")
			}
		}
		if r.it.Active != nil && !r.it.Active.ExpiresOn.IsZero() {
			fmt.Fprintf(&s, " · exp %s", longDate(r.it.Active.ExpiresOn))
		}
		if r.it.Spares > 0 {
			fmt.Fprintf(&s, " · 📦 %d cadangan", r.it.Spares)
		}
	}
	return s.String()
}
