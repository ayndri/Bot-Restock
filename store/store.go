// Package store menyimpan barang dan unitnya di Postgres (Neon).
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/ayndri/restok/restock"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL tidak valid: %w", err)
	}
	// Fungsi serverless bisa jalan di banyak instance sekaligus. Koneksi per
	// instance dibuat sedikit supaya kuota koneksi Neon tidak habis.
	cfg.MaxConns = 2
	// Tanpa cache prepared statement, biar aman lewat connection pooler Neon.
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	// schema.sql berisi beberapa statement; hanya simple protocol yang bisa.
	_, err := s.pool.Exec(ctx, schema, pgx.QueryExecModeSimpleProtocol)
	return err
}

// dateParam mengirim tanggal sebagai teks "2006-01-02" untuk kolom DATE.
// time.Time akan dikirim sebagai timestamptz lalu dipotong di zona sesi
// (UTC), sehingga tengah malam WIB mundur sehari.
func dateParam(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.In(restock.WIB).Format(time.DateOnly)
	return &s
}

// wibDate mengubah hasil scan kolom DATE (tengah malam UTC) jadi tengah malam WIB.
func wibDate(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, restock.WIB)
}

// Buy mencatat satu unit baru. Kalau barang itu sedang tidak dipakai, unit
// langsung dianggap mulai dipakai; kalau masih ada, unit jadi cadangan.
// expires nol berarti tanggal kadaluwarsa tidak dicatat.
func (s *Store) Buy(ctx context.Context, chatID, updateID int64, name string, price int64, at, expires time.Time) (restock.BuyResult, error) {
	var res restock.BuyResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO items (chat_id, name) VALUES ($1, $2)
			ON CONFLICT (chat_id, name) DO NOTHING
			RETURNING id`, chatID, name).Scan(&res.ItemID)
		switch {
		case err == nil:
			res.NewItem = true
		case errors.Is(err, pgx.ErrNoRows):
			err = tx.QueryRow(ctx, `
				SELECT id FROM items WHERE chat_id = $1 AND name = $2 FOR UPDATE`,
				chatID, name).Scan(&res.ItemID)
		}
		if err != nil {
			return err
		}

		if _, ok, err := activeUnit(ctx, tx, res.ItemID); err != nil {
			return err
		} else if ok {
			res.Spare = true
		}
		var opened *time.Time
		if !res.Spare {
			opened = &at
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO units (item_id, price, bought_at, opened_at, expires_on) VALUES ($1, $2, $3, $4, $5::date)
			RETURNING id`, res.ItemID, price, at, opened, dateParam(expires)).Scan(&res.UnitID); err != nil {
			return err
		}
		return logAction(ctx, tx, chatID, updateID, "buy", res.UnitID, 0, 0)
	})
	return res, err
}

// Finish menutup unit yang sedang dipakai, lalu membuka cadangan yang paling
// cepat kadaluwarsa. Cadangan tanpa tanggal kadaluwarsa dibuka dari yang tertua.
func (s *Store) Finish(ctx context.Context, chatID, updateID, itemID int64, at time.Time) (restock.FinishResult, error) {
	var res restock.FinishResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Kunci barangnya dulu supaya dua laporan "habis" bersamaan tidak
		// sama-sama membuka cadangan.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM items WHERE id = $1 AND chat_id = $2 FOR UPDATE`, itemID, chatID); err != nil {
			return err
		}
		u, ok, err := activeUnit(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if !ok {
			res.NothingActive = true
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE units SET finished_at = $2 WHERE id = $1`, u.id, at); err != nil {
			return err
		}
		res.Days = restock.CycleDays(u.opened, at)

		var spareID int64
		err = tx.QueryRow(ctx, `
			UPDATE units SET opened_at = $2 WHERE id = (
				SELECT id FROM units WHERE item_id = $1 AND opened_at IS NULL
				ORDER BY expires_on NULLS LAST, bought_at, id LIMIT 1
			)
			RETURNING id`, itemID, at).Scan(&spareID)
		switch {
		case err == nil:
			res.OpenedSpare = true
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		return logAction(ctx, tx, chatID, updateID, "finish", u.id, spareID, 0)
	})
	return res, err
}

type unitRef struct {
	id     int64
	opened time.Time
}

func activeUnit(ctx context.Context, tx pgx.Tx, itemID int64) (unitRef, bool, error) {
	var u unitRef
	err := tx.QueryRow(ctx, `
		SELECT id, opened_at FROM units
		WHERE item_id = $1 AND opened_at IS NOT NULL AND finished_at IS NULL
		FOR UPDATE`, itemID).Scan(&u.id, &u.opened)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, false, nil
	}
	return u, err == nil, err
}

// logAction mencatat aksi untuk /batal. Kalau update yang sama datang lagi
// (Telegram mengirim ulang saat webhook lambat), hasilnya restock.ErrDuplicate
// dan seluruh transaksi dibatalkan. Aksi barang mengisi unitID, aksi kegiatan
// mengisi logID; id 0 disimpan sebagai NULL.
func logAction(ctx context.Context, tx pgx.Tx, chatID, updateID int64, kind string, unitID, openedUnitID, logID int64) error {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO actions (chat_id, update_id, kind, unit_id, opened_unit_id, log_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (chat_id, update_id) DO NOTHING
		RETURNING id`, chatID, updateID, kind, nullID(unitID), nullID(openedUnitID), nullID(logID)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return restock.ErrDuplicate
	}
	return err
}

func nullID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

// Undo membatalkan aksi terakhir. ok=false kalau belum ada aksi.
func (s *Store) Undo(ctx context.Context, chatID int64) (restock.Undone, bool, error) {
	var res restock.Undone
	var ok bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var actionID, ownerID int64
		var unitID, openedUnitID, logID *int64
		var kind string
		// ownerID adalah id barang untuk aksi beli/habis, id kegiatan untuk "done".
		err := tx.QueryRow(ctx, `
			SELECT a.id, a.kind, a.unit_id, a.opened_unit_id, a.log_id,
			       COALESCE(i.id, h.id), COALESCE(i.name, h.name)
			FROM actions a
			LEFT JOIN units u ON u.id = a.unit_id
			LEFT JOIN items i ON i.id = u.item_id
			LEFT JOIN habit_logs l ON l.id = a.log_id
			LEFT JOIN habits h ON h.id = l.habit_id
			WHERE a.chat_id = $1
			ORDER BY a.id DESC LIMIT 1
			FOR UPDATE OF a`, chatID).Scan(&actionID, &kind, &unitID, &openedUnitID, &logID, &ownerID, &res.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		res.Action = restock.Action(kind)

		switch res.Action {
		case restock.Buy:
			// Menghapus unit ikut menghapus baris aksinya (ON DELETE CASCADE).
			if _, err := tx.Exec(ctx, `DELETE FROM units WHERE id = $1`, *unitID); err != nil {
				return err
			}
			// Barang yang tidak punya unit sama sekali ikut dihapus, supaya
			// salah ketik nama tidak tertinggal di /stok.
			_, err := tx.Exec(ctx, `
				DELETE FROM items WHERE id = $1
				AND NOT EXISTS (SELECT 1 FROM units WHERE item_id = $1)`, ownerID)
			return err
		case restock.Done:
			// Sama seperti beli: catatannya dihapus, kegiatan tanpa catatan ikut hilang.
			if _, err := tx.Exec(ctx, `DELETE FROM habit_logs WHERE id = $1`, *logID); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				DELETE FROM habits WHERE id = $1
				AND NOT EXISTS (SELECT 1 FROM habit_logs WHERE habit_id = $1)`, ownerID)
			return err
		}

		// Cadangan ditutup dulu sebelum unit lama dibuka lagi, supaya tidak
		// melanggar aturan satu unit dipakai per barang.
		if openedUnitID != nil {
			if _, err := tx.Exec(ctx, `UPDATE units SET opened_at = NULL WHERE id = $1`, *openedUnitID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE units SET finished_at = NULL WHERE id = $1`, *unitID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM actions WHERE id = $1`, actionID)
		return err
	})
	return res, ok, err
}

// Items mengembalikan semua barang milik chat beserta keadaannya.
func (s *Store) Items(ctx context.Context, chatID int64) ([]restock.Item, error) {
	return s.loadItems(ctx, chatID, 0)
}

// Item mengembalikan satu barang. ok=false kalau tidak ada atau milik chat lain.
func (s *Store) Item(ctx context.Context, chatID, itemID int64) (restock.Item, bool, error) {
	items, err := s.loadItems(ctx, chatID, itemID)
	if err != nil || len(items) == 0 {
		return restock.Item{}, false, err
	}
	return items[0], true, nil
}

// ItemByUnit mengembalikan barang pemilik unit tertentu.
func (s *Store) ItemByUnit(ctx context.Context, chatID, unitID int64) (restock.Item, bool, error) {
	var itemID int64
	err := s.pool.QueryRow(ctx, `
		SELECT i.id FROM units u JOIN items i ON i.id = u.item_id
		WHERE u.id = $1 AND i.chat_id = $2`, unitID, chatID).Scan(&itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return restock.Item{}, false, nil
	}
	if err != nil {
		return restock.Item{}, false, err
	}
	return s.Item(ctx, chatID, itemID)
}

// loadItems memuat barang dan semua unitnya, lalu merangkumnya di Go.
// itemID 0 berarti semua barang. Bot ini pribadi, jadi jumlah barisnya kecil.
func (s *Store) loadItems(ctx context.Context, chatID, itemID int64) ([]restock.Item, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(guess_days, 0), remind_before
		FROM items WHERE chat_id = $1 AND ($2 = 0 OR id = $2)
		ORDER BY name`, chatID, itemID)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (restock.Item, error) {
		var it restock.Item
		err := row.Scan(&it.ID, &it.Name, &it.GuessDays, &it.RemindBefore)
		return it, err
	})
	if err != nil || len(items) == 0 {
		return items, err
	}
	byID := make(map[int64]*restock.Item, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}

	// Urut berdasarkan waktu habis supaya Cycles ikut urut kronologis, lalu
	// cadangan sesuai urutan dibukanya (sama dengan Finish).
	rows, err = s.pool.Query(ctx, `
		SELECT u.id, u.item_id, u.price, u.bought_at, u.opened_at, u.finished_at, u.reminded_at, u.snooze_until,
		       u.expires_on, u.expiry_reminded_at
		FROM units u JOIN items i ON i.id = u.item_id
		WHERE i.chat_id = $1 AND ($2 = 0 OR i.id = $2)
		ORDER BY u.finished_at NULLS LAST, u.expires_on NULLS LAST, u.bought_at, u.id`, chatID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u restock.Unit
		var opened, finished, reminded, snooze, expires, expReminded *time.Time
		if err := rows.Scan(&u.ID, &u.ItemID, &u.Price, &u.BoughtAt, &opened, &finished, &reminded, &snooze,
			&expires, &expReminded); err != nil {
			return nil, err
		}
		u.ExpiresOn = wibDate(expires)
		if expReminded != nil {
			u.ExpiryRemindedAt = *expReminded
		}
		it := byID[u.ItemID]
		switch {
		case opened == nil:
			it.Spares++
			it.SpareUnits = append(it.SpareUnits, u)
		case finished != nil:
			it.Cycles = append(it.Cycles, restock.CycleDays(*opened, *finished))
		default:
			u.OpenedAt = *opened
			if reminded != nil {
				u.RemindedAt = *reminded
			}
			if snooze != nil {
				u.SnoozeUntil = *snooze
			}
			it.Active = &u
		}
	}
	return items, rows.Err()
}

func (s *Store) SetGuess(ctx context.Context, chatID, itemID int64, days int) (bool, error) {
	var guess *int
	if days > 0 {
		guess = &days
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE items SET guess_days = $3 WHERE id = $1 AND chat_id = $2`, itemID, chatID, guess)
	return tag.RowsAffected() > 0, err
}

func (s *Store) MarkReminded(ctx context.Context, unitID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE units SET reminded_at = $2 WHERE id = $1`, unitID, at)
	return err
}

// Snooze menunda pengingat unit sampai tanggal tertentu.
func (s *Store) Snooze(ctx context.Context, unitID int64, until time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE units SET snooze_until = $2, reminded_at = NULL WHERE id = $1`, unitID, until)
	return err
}

// SetExpiry mengatur tanggal kadaluwarsa satu unit. ok=false kalau unit tidak
// ada atau milik chat lain.
func (s *Store) SetExpiry(ctx context.Context, chatID, unitID int64, day time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE units u SET expires_on = $3::date, expiry_reminded_at = NULL
		FROM items i WHERE u.id = $1 AND i.id = u.item_id AND i.chat_id = $2`,
		unitID, chatID, dateParam(day))
	return tag.RowsAffected() > 0, err
}

func (s *Store) MarkExpiryReminded(ctx context.Context, unitID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE units SET expiry_reminded_at = $2 WHERE id = $1`, unitID, at)
	return err
}

// SetPending mencatat bahwa pesan berikutnya dari chat ini adalah tanggal
// kadaluwarsa untuk unit tersebut. Unit lain yang sedang ditunggu diganti.
func (s *Store) SetPending(ctx context.Context, chatID, unitID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO pending_expiry (chat_id, unit_id, created_at) VALUES ($1, $2, $3)
		ON CONFLICT (chat_id) DO UPDATE SET unit_id = EXCLUDED.unit_id, created_at = EXCLUDED.created_at`,
		chatID, unitID, at)
	return err
}

// TakePending mengambil sekaligus menghapus unit yang sedang ditunggu.
func (s *Store) TakePending(ctx context.Context, chatID int64) (unitID int64, since time.Time, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `
		DELETE FROM pending_expiry WHERE chat_id = $1 RETURNING unit_id, created_at`,
		chatID).Scan(&unitID, &since)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, time.Time{}, false, nil
	}
	return unitID, since, err == nil, err
}

// Chats mengembalikan semua chat yang punya barang atau kegiatan.
func (s *Store) Chats(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT chat_id FROM items UNION SELECT chat_id FROM habits`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
