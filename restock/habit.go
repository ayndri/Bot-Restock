package restock

import "time"

// Habit adalah kegiatan rutin yang dicatat "terakhir kali"-nya, misalnya
// potong rambut atau ganti sprei. Siklusnya jarak antar catatan, dan
// perkiraannya memakai Predict yang sama dengan barang.
type Habit struct {
	ID        int64
	Name      string
	GuessDays int // tebakan awal dari pengguna; 0 = belum ada
	// Logs berisi catatan kegiatan, urut dari yang paling lama.
	Logs []HabitLog
}

// HabitLog adalah satu kali kegiatan dilakukan. Status pengingat disimpan di
// catatan terakhir, jadi catatan baru otomatis memulai pengingat dari nol.
type HabitLog struct {
	ID     int64
	DoneAt time.Time
	// RemindedAt tidak nol berarti pengingat sejak catatan ini sudah dikirim.
	RemindedAt  time.Time
	SnoozeUntil time.Time
}

// HabitSnoozeDays adalah jeda sebelum bertanya lagi setelah "Nanti dulu".
// Lebih panjang dari barang karena menunda potong rambut tidak bikin kehabisan apa-apa.
const HabitSnoozeDays = 7

// Last mengembalikan catatan terbaru; nil kalau belum ada.
func (h Habit) Last() *HabitLog {
	if len(h.Logs) == 0 {
		return nil
	}
	return &h.Logs[len(h.Logs)-1]
}

// Intervals adalah jarak (hari) antar catatan berurutan, dari yang paling lama.
func (h Habit) Intervals() []int {
	var out []int
	for i := 1; i < len(h.Logs); i++ {
		out = append(out, CycleDays(h.Logs[i-1].DoneAt, h.Logs[i].DoneAt))
	}
	return out
}

// Cycle mengembalikan perkiraan jarak antar kegiatan dalam hari.
// ok=false kalau belum ada riwayat maupun tebakan.
func (h Habit) Cycle() (days int, ok bool) {
	return Predict(h.Intervals(), h.GuessDays)
}

// DueDate adalah perkiraan kapan kegiatan ini waktunya dilakukan lagi.
func (h Habit) DueDate() (time.Time, bool) {
	last := h.Last()
	if last == nil {
		return time.Time{}, false
	}
	days, ok := h.Cycle()
	if !ok {
		return time.Time{}, false
	}
	return StartOfDay(last.DoneAt).AddDate(0, 0, days), true
}

// NeedsReminder menentukan apakah pengingat dikirim hari ini. Berbeda dengan
// barang, pengingat dikirim tepat di hari perkiraannya: tidak ada yang perlu
// dibeli dulu, jadi tidak perlu diingatkan lebih awal.
func (h Habit) NeedsReminder(today time.Time) bool {
	last := h.Last()
	if last == nil || !last.RemindedAt.IsZero() {
		return false
	}
	if !last.SnoozeUntil.IsZero() && today.Before(last.SnoozeUntil) {
		return false
	}
	due, ok := h.DueDate()
	return ok && !today.Before(due)
}

// DoneResult menjelaskan apa yang terjadi saat kegiatan dicatat.
type DoneResult struct {
	HabitID  int64
	LogID    int64
	NewHabit bool // kegiatan ini baru pertama kali dicatat
	// SameDay berarti sudah ada catatan di tanggal yang sama; tidak ada yang
	// disimpan, supaya jarak 0–1 hari tidak merusak perkiraan.
	SameDay bool
}
