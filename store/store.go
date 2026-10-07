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

// Buy mencatat satu unit baru. Kalau barang itu sedang tidak dipakai, unit
// langsung dianggap mulai dipakai; kalau masih ada, unit jadi cadangan.
func (s *Store) Buy(ctx context.Context, chatID, updateID int64, name string, price int64, at time.Time) (restock.BuyResult, error) {
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

		var unitID int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO units (item_id, price, bought_at, opened_at) VALUES ($1, $2, $3, $4)
			RETURNING id`, res.ItemID, price, at, opened).Scan(&unitID); err != nil {
			return err
		}
		return logAction(ctx, tx, chatID, updateID, "buy", unitID, 0)
	})
	return res, err
}

// Finish menutup unit yang sedang dipakai, lalu membuka cadangan tertua kalau ada.
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
				ORDER BY bought_at, id LIMIT 1
			)
			RETURNING id`, itemID, at).Scan(&spareID)
		switch {
		case err == nil:
			res.OpenedSpare = true
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		return logAction(ctx, tx, chatID, updateID, "finish", u.id, spareID)
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
// dan seluruh transaksi dibatalkan.
func logAction(ctx context.Context, tx pgx.Tx, chatID, updateID int64, kind string, unitID, openedUnitID int64) error {
	var opened *int64
	if openedUnitID != 0 {
		opened = &openedUnitID
	}
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO actions (chat_id, update_id, kind, unit_id, opened_unit_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (chat_id, update_id) DO NOTHING
		RETURNING id`, chatID, updateID, kind, unitID, opened).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return restock.ErrDuplicate
	}
	return err
}

// Undo membatalkan aksi terakhir. ok=false kalau belum ada aksi.
func (s *Store) Undo(ctx context.Context, chatID int64) (restock.Undone, bool, error) {
	var res restock.Undone
	var ok bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var actionID, unitID, itemID int64
		var openedUnitID *int64
		var kind string
		err := tx.QueryRow(ctx, `
			SELECT a.id, a.kind, a.unit_id, a.opened_unit_id, i.id, i.name
			FROM actions a
			JOIN units u ON u.id = a.unit_id
			JOIN items i ON i.id = u.item_id
			WHERE a.chat_id = $1
			ORDER BY a.id DESC LIMIT 1
			FOR UPDATE OF a`, chatID).Scan(&actionID, &kind, &unitID, &openedUnitID, &itemID, &res.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		res.Action = restock.Action(kind)

		if res.Action == restock.Buy {
			// Menghapus unit ikut menghapus baris aksinya (ON DELETE CASCADE).
			if _, err := tx.Exec(ctx, `DELETE FROM units WHERE id = $1`, unitID); err != nil {
				return err
			}
			// Barang yang tidak punya unit sama sekali ikut dihapus, supaya
			// salah ketik nama tidak tertinggal di /stok.
			_, err := tx.Exec(ctx, `
				DELETE FROM items WHERE id = $1
				AND NOT EXISTS (SELECT 1 FROM units WHERE item_id = $1)`, itemID)
			return err
		}

		// Cadangan ditutup dulu sebelum unit lama dibuka lagi, supaya tidak
		// melanggar aturan satu unit dipakai per barang.
		if openedUnitID != nil {
			if _, err := tx.Exec(ctx, `UPDATE units SET opened_at = NULL WHERE id = $1`, *openedUnitID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE units SET finished_at = NULL WHERE id = $1`, unitID); err != nil {
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

	// Urut berdasarkan waktu habis supaya Cycles ikut urut kronologis.
	rows, err = s.pool.Query(ctx, `
		SELECT u.id, u.item_id, u.price, u.bought_at, u.opened_at, u.finished_at, u.reminded_at, u.snooze_until
		FROM units u JOIN items i ON i.id = u.item_id
		WHERE i.chat_id = $1 AND ($2 = 0 OR i.id = $2)
		ORDER BY u.finished_at NULLS LAST, u.id`, chatID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u restock.Unit
		var opened, finished, reminded, snooze *time.Time
		if err := rows.Scan(&u.ID, &u.ItemID, &u.Price, &u.BoughtAt, &opened, &finished, &reminded, &snooze); err != nil {
			return nil, err
		}
		it := byID[u.ItemID]
		switch {
		case opened == nil:
			it.Spares++
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

// Chats mengembalikan semua chat yang punya barang.
func (s *Store) Chats(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT chat_id FROM items`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
