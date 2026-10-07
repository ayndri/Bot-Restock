// Package restock berisi aturan inti: membaca pesan, menghitung siklus
// pemakaian, dan menentukan kapan sebuah barang perlu diingatkan. Tidak tahu
// apa-apa soal Telegram maupun database, jadi gampang dites.
package restock

import (
	"errors"
	"time"
)

// WIB tidak punya daylight saving, jadi zona tetap sudah cukup dan tidak
// bergantung pada tzdata di server.
var WIB = time.FixedZone("WIB", 7*60*60)

// DefaultRemindBefore adalah berapa hari sebelum perkiraan habis bot mulai
// mengingatkan.
const DefaultRemindBefore = 5

// ErrDuplicate dikembalikan saat Telegram mengirim ulang update yang sama.
var ErrDuplicate = errors.New("update sudah pernah diproses")

// Unit adalah satu barang fisik. Beli dua sabun berarti dua Unit.
type Unit struct {
	ID       int64
	ItemID   int64
	Price    int64 // 0 = tidak dicatat
	BoughtAt time.Time
	// OpenedAt nol berarti masih cadangan, belum dipakai.
	OpenedAt time.Time
	// RemindedAt tidak nol berarti pengingat untuk unit ini sudah dikirim.
	RemindedAt  time.Time
	SnoozeUntil time.Time
}

// Item adalah satu jenis barang beserta keadaannya saat ini.
type Item struct {
	ID           int64
	Name         string
	GuessDays    int // tebakan awal dari pengguna; 0 = belum ada
	RemindBefore int
	// Active adalah unit yang sedang dipakai; nil kalau tidak ada.
	Active *Unit
	Spares int
	// Cycles berisi lama pemakaian (hari) unit-unit yang sudah habis,
	// urut dari yang paling lama habisnya.
	Cycles []int
}

// Cycle mengembalikan perkiraan lama pemakaian satu unit dalam hari.
// ok=false kalau belum ada riwayat maupun tebakan.
func (it Item) Cycle() (days int, ok bool) {
	return Predict(it.Cycles, it.GuessDays)
}

// DueDate adalah perkiraan tanggal unit yang sedang dipakai habis.
func (it Item) DueDate() (time.Time, bool) {
	if it.Active == nil {
		return time.Time{}, false
	}
	days, ok := it.Cycle()
	if !ok {
		return time.Time{}, false
	}
	return StartOfDay(it.Active.OpenedAt).AddDate(0, 0, days), true
}

// NeedsReminder menentukan apakah pengingat "mau habis" dikirim hari ini.
// Barang yang masih punya cadangan tidak perlu diingatkan.
func (it Item) NeedsReminder(today time.Time) bool {
	if it.Active == nil || it.Spares > 0 || !it.Active.RemindedAt.IsZero() {
		return false
	}
	if !it.Active.SnoozeUntil.IsZero() && today.Before(it.Active.SnoozeUntil) {
		return false
	}
	due, ok := it.DueDate()
	if !ok {
		return false
	}
	before := it.RemindBefore
	if before <= 0 {
		before = DefaultRemindBefore
	}
	return !today.Before(due.AddDate(0, 0, -before))
}

// BuyResult menjelaskan apa yang terjadi saat satu unit dibeli.
type BuyResult struct {
	ItemID  int64
	NewItem bool // barang ini baru pertama kali dicatat
	Spare   bool // disimpan sebagai cadangan karena masih ada yang dipakai
}

// FinishResult menjelaskan apa yang terjadi saat barang dilaporkan habis.
type FinishResult struct {
	NothingActive bool // tidak ada unit yang sedang dipakai
	Days          int  // lama pemakaian unit yang habis
	OpenedSpare   bool // cadangan otomatis mulai dipakai
}

// Undone menjelaskan aksi yang dibatalkan lewat /batal.
type Undone struct {
	Action Action
	Name   string
}

// StartOfDay mengembalikan tengah malam WIB di hari t.
func StartOfDay(t time.Time) time.Time {
	y, m, d := t.In(WIB).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, WIB)
}

// DaysBetween menghitung selisih tanggal kalender WIB, bukan selisih 24 jam:
// dibuka Senin malam dan habis Selasa pagi tetap dihitung 1 hari.
func DaysBetween(from, to time.Time) int {
	return int(StartOfDay(to).Sub(StartOfDay(from)).Hours()) / 24
}

// CycleDays adalah lama pemakaian satu unit. Habis di hari yang sama dengan
// dibuka tetap dihitung 1 hari.
func CycleDays(opened, finished time.Time) int {
	return max(1, DaysBetween(opened, finished))
}
