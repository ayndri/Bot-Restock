package bot

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ayndri/restok/restock"
	"github.com/ayndri/restok/telegram"
)

// Kegiatan rutin ("udah potong rambut") memakai pola yang sama dengan barang:
// catat, pelajari jaraknya, ingatkan saat waktunya. Bedanya, yang dicatat
// cuma tanggal, dan pengingat dikirim tepat di hari perkiraannya.

var habitGuessOptions = []struct {
	label string
	days  int
}{
	{"1 minggu", 7}, {"2 minggu", 14}, {"1 bulan", 30}, {"2 bulan", 60},
	{"3 bulan", 90}, {"6 bulan", 180}, {"1 tahun", 365}, {"Nggak tahu", 0},
}

func habitGuessKeyboard(habitID int64) *telegram.InlineKeyboardMarkup {
	var rows [][]telegram.InlineKeyboardButton
	for i := 0; i < len(habitGuessOptions); i += 2 {
		var row []telegram.InlineKeyboardButton
		for _, o := range habitGuessOptions[i:min(i+2, len(habitGuessOptions))] {
			row = append(row, button(o.label, fmt.Sprintf("hg:%d:%d", habitID, o.days)))
		}
		rows = append(rows, row)
	}
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func habitReminderKeyboard(logID int64) *telegram.InlineKeyboardMarkup {
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
		button("✅ Udah", fmt.Sprintf("hd:%d", logID)),
		button("⏳ Nanti dulu", fmt.Sprintf("hn:%d", logID)),
	}}}
}

func (b *Bot) done(ctx context.Context, updateID, chatID int64, name string, at time.Time) (Reply, error) {
	res, err := b.Store.Done(ctx, chatID, updateID, name, at)
	if err != nil {
		return Reply{}, err
	}
	h, _, err := b.Store.Habit(ctx, chatID, res.HabitID)
	if err != nil {
		return Reply{}, err
	}
	if res.SameDay {
		when := "hari ini"
		if b.backdated(at) != "" {
			when = "tanggal " + shortDate(at)
		}
		return text(fmt.Sprintf("%s udah kecatat %s, jadi nggak aku catat dua kali.", bold(h.Name), when)), nil
	}

	s := formatDone(h, res.LogID, b.backdated(at))
	if res.NewHabit {
		return Reply{Text: s + "\n\nIni pertama kali kamu catat. Biasanya berapa lama sekali?",
			Keyboard: habitGuessKeyboard(h.ID)}, nil
	}
	return text(s + "\n\nSalah ketik? /batal"), nil
}

// habitCallback menangani tombol hg (tebakan), hd (udah), dan hn (nanti dulu).
func (b *Bot) habitCallback(ctx context.Context, updateID, chatID, id int64, action, arg string,
	edit func(string) error) (string, error) {
	if action == "hg" {
		days, err := strconv.Atoi(arg)
		if err != nil || days < 0 {
			return "", nil
		}
		ok, err := b.Store.SetHabitGuess(ctx, chatID, id, days)
		if err != nil || !ok {
			return "", err
		}
		h, _, err := b.Store.Habit(ctx, chatID, id)
		if err != nil {
			return "", err
		}
		return "", edit(formatHabitGuessSet(h, days))
	}

	h, ok, err := b.Store.HabitByLog(ctx, chatID, id)
	if err != nil {
		return "", err
	}
	// Sudah ada catatan yang lebih baru, atau catatannya dibatalkan.
	if !ok || h.Last().ID != id {
		return "Pengingat ini udah nggak berlaku.", edit("<i>Pengingat ini udah nggak berlaku.</i>")
	}
	if action == "hn" {
		until := startOfDay(b.now()).AddDate(0, 0, restock.HabitSnoozeDays)
		if err := b.Store.SnoozeHabit(ctx, id, until); err != nil {
			return "", err
		}
		return "", edit(fmt.Sprintf("⏳ Oke, %s nanti dulu. Aku tanya lagi %d hari lagi.", bold(h.Name), restock.HabitSnoozeDays))
	}
	r, err := b.done(ctx, updateID, chatID, h.Name, b.now())
	if err != nil {
		return "", err
	}
	return "Dicatat ✅", edit(r.Text)
}

// dateLine kosong untuk catatan hari ini, atau "\n📅 Dicatat untuk tanggal ...".
func formatDone(h restock.Habit, logID int64, dateLine string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "✅ %s dicatat%s", bold(h.Name), dateLine)
	// Jarak hanya ditampilkan kalau catatan ini yang terbaru; catatan mundur
	// yang terselip di tengah riwayat tidak punya "sebelumnya" yang jelas.
	if n := len(h.Logs); n >= 2 && h.Logs[n-1].ID == logID {
		fmt.Fprintf(&s, "\nTerakhir sebelumnya %s, jaraknya <b>%d hari</b>.",
			shortDate(h.Logs[n-2].DoneAt), restock.CycleDays(h.Logs[n-2].DoneAt, h.Logs[n-1].DoneAt))
	}
	if due, ok := h.DueDate(); ok {
		days, _ := h.Cycle()
		fmt.Fprintf(&s, "\n⏳ Waktunya lagi sekitar <b>%s</b> (±%d hari)", shortDate(due), days)
	}
	return s.String()
}

func formatHabitGuessSet(h restock.Habit, days int) string {
	if days == 0 {
		return fmt.Sprintf("✅ %s dicatat\n\nOke, nanti aku pelajari sendiri setelah kamu catat sekali lagi.", bold(h.Name))
	}
	s := fmt.Sprintf("✅ %s dicatat\n\nOke, aku anggap tiap ±%d hari.", bold(h.Name), days)
	if due, ok := h.DueDate(); ok {
		s += fmt.Sprintf(" Aku ingetin sekitar <b>%s</b>.", shortDate(due))
	}
	return s + "\nMakin sering dicatat, jaraknya aku sesuaikan sendiri."
}

func formatHabitReminder(h restock.Habit, today time.Time) string {
	last := h.Last()
	days, _ := h.Cycle()
	return fmt.Sprintf("🔔 Waktunya %s lagi.\nTerakhir %s, udah <b>%d hari</b> (biasanya ±%d hari).\n\nUdah dilakukan?",
		bold(h.Name), shortDate(last.DoneAt), restock.DaysBetween(last.DoneAt, today), days)
}

// formatHabits mengurutkan kegiatan dari yang paling cepat waktunya.
// Kegiatan tanpa perkiraan menyusul di bawah.
func formatHabits(habits []restock.Habit, today time.Time) string {
	if len(habits) == 0 {
		return "Belum ada kegiatan. Coba kirim <code>udah potong rambut</code>."
	}

	type row struct {
		h   restock.Habit
		due time.Time
		ok  bool
	}
	rows := make([]row, 0, len(habits))
	for _, h := range habits {
		due, ok := h.DueDate()
		rows = append(rows, row{h, due, ok})
	}
	slices.SortStableFunc(rows, func(a, b row) int {
		if a.ok != b.ok {
			if a.ok {
				return -1
			}
			return 1
		}
		return a.due.Compare(b.due)
	})

	var s strings.Builder
	s.WriteString("<b>🔁 Kegiatan rutin</b>\n")
	for _, r := range rows {
		last := r.h.Last()
		fmt.Fprintf(&s, "\n• %s — ", bold(r.h.Name))
		if r.ok {
			fmt.Fprintf(&s, "waktunya ±%s (%s) · terakhir %s", shortDate(r.due), relDay(r.due, today), shortDate(last.DoneAt))
		} else {
			fmt.Fprintf(&s, "terakhir %s, belum ada perkiraan", shortDate(last.DoneAt))
		}
	}
	return s.String()
}
