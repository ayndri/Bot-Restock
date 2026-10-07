package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ayndri/restok/restock"
)

// Tes ini butuh Postgres sungguhan dan dilewati kalau TEST_DATABASE_URL
// kosong. Isinya dihapus tiap kali jalan, jadi jangan arahkan ke database
// produksi.
func openTest(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL kosong")
	}
	ctx := context.Background()
	st, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pool.Exec(ctx, `TRUNCATE actions, units, items RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestStoreFlow(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	const chat = 42
	day := func(d int) time.Time { return time.Date(2026, 9, d, 9, 0, 0, 0, restock.WIB) }

	// Migrate aman dijalankan dua kali.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate kedua: %v", err)
	}

	res, err := st.Buy(ctx, chat, 1, "serum", 120_000, day(1))
	if err != nil || !res.NewItem || res.Spare {
		t.Fatalf("beli pertama: %+v, %v", res, err)
	}
	if _, err := st.Buy(ctx, chat, 1, "serum", 120_000, day(1)); !errors.Is(err, restock.ErrDuplicate) {
		t.Fatalf("update dobel: err = %v, mau ErrDuplicate", err)
	}
	res, err = st.Buy(ctx, chat, 2, "serum", 0, day(2))
	if err != nil || res.NewItem || !res.Spare {
		t.Fatalf("beli kedua: %+v, %v", res, err)
	}
	if ok, err := st.SetGuess(ctx, chat, res.ItemID, 30); !ok || err != nil {
		t.Fatalf("SetGuess: %v, %v", ok, err)
	}
	if ok, _ := st.SetGuess(ctx, chat+1, res.ItemID, 30); ok {
		t.Fatal("chat lain bisa mengubah barang ini")
	}

	it, ok, err := st.Item(ctx, chat, res.ItemID)
	if err != nil || !ok || it.Active == nil || it.Spares != 1 || it.GuessDays != 30 || it.Active.Price != 120_000 {
		t.Fatalf("Item: %+v (aktif %+v), %v, %v", it, it.Active, ok, err)
	}
	firstUnit := it.Active.ID

	fin, err := st.Finish(ctx, chat, 3, res.ItemID, day(21))
	if err != nil || fin.Days != 20 || !fin.OpenedSpare {
		t.Fatalf("Finish: %+v, %v", fin, err)
	}
	it, _, _ = st.Item(ctx, chat, res.ItemID)
	if it.Active == nil || it.Active.ID == firstUnit || it.Spares != 0 || len(it.Cycles) != 1 || it.Cycles[0] != 20 {
		t.Fatalf("setelah habis: %+v", it)
	}

	// Pengingat dan tunda.
	if err := st.MarkReminded(ctx, it.Active.ID, day(22)); err != nil {
		t.Fatal(err)
	}
	if err := st.Snooze(ctx, it.Active.ID, day(25)); err != nil {
		t.Fatal(err)
	}
	it, _, _ = st.ItemByUnit(ctx, chat, it.Active.ID)
	if !it.Active.RemindedAt.IsZero() || !it.Active.SnoozeUntil.Equal(day(25)) {
		t.Fatalf("tunda: %+v", it.Active)
	}
	if _, ok, _ := st.ItemByUnit(ctx, chat+1, it.Active.ID); ok {
		t.Fatal("chat lain bisa melihat unit ini")
	}

	// /batal mengembalikan keadaan sebelum habis.
	u, ok, err := st.Undo(ctx, chat)
	if err != nil || !ok || u.Action != restock.Finish || u.Name != "serum" {
		t.Fatalf("Undo habis: %+v, %v, %v", u, ok, err)
	}
	it, _, _ = st.Item(ctx, chat, res.ItemID)
	if it.Active == nil || it.Active.ID != firstUnit || it.Spares != 1 || len(it.Cycles) != 0 {
		t.Fatalf("setelah undo habis: %+v", it)
	}

	// Membatalkan dua pembelian menghapus barangnya sekalian.
	for range 2 {
		if u, ok, err := st.Undo(ctx, chat); err != nil || !ok || u.Action != restock.Buy {
			t.Fatalf("Undo beli: %+v, %v, %v", u, ok, err)
		}
	}
	if items, _ := st.Items(ctx, chat); len(items) != 0 {
		t.Fatalf("barang masih ada: %+v", items)
	}
	if _, ok, _ := st.Undo(ctx, chat); ok {
		t.Fatal("Undo saat kosong seharusnya ok=false")
	}

	// Habis tanpa unit yang dipakai.
	res, _ = st.Buy(ctx, chat, 10, "shampo", 0, day(1))
	st.Finish(ctx, chat, 11, res.ItemID, day(15))
	if fin, err := st.Finish(ctx, chat, 12, res.ItemID, day(16)); err != nil || !fin.NothingActive {
		t.Fatalf("habis dua kali: %+v, %v", fin, err)
	}

	chats, err := st.Chats(ctx)
	if err != nil || len(chats) != 1 || chats[0] != chat {
		t.Fatalf("Chats: %v, %v", chats, err)
	}
}
