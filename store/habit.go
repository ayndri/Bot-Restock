package store

import (
	"context"
	"errors"
	"time"

	"github.com/ayndri/restok/restock"
	"github.com/jackc/pgx/v5"
)

// Done mencatat satu kali kegiatan. Kalau di tanggal yang sama (WIB) sudah
// ada catatan, tidak ada yang disimpan dan res.SameDay bernilai true.
func (s *Store) Done(ctx context.Context, chatID, updateID int64, name string, at time.Time) (restock.DoneResult, error) {
	var res restock.DoneResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Kiriman ulang dicek duluan, supaya tidak dibalas "udah kecatat".
		var seen bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM actions WHERE chat_id = $1 AND update_id = $2)`,
			chatID, updateID).Scan(&seen); err != nil {
			return err
		}
		if seen {
			return restock.ErrDuplicate
		}

		err := tx.QueryRow(ctx, `
			INSERT INTO habits (chat_id, name) VALUES ($1, $2)
			ON CONFLICT (chat_id, name) DO NOTHING
			RETURNING id`, chatID, name).Scan(&res.HabitID)
		switch {
		case err == nil:
			res.NewHabit = true
		case errors.Is(err, pgx.ErrNoRows):
			err = tx.QueryRow(ctx, `
				SELECT id FROM habits WHERE chat_id = $1 AND name = $2 FOR UPDATE`,
				chatID, name).Scan(&res.HabitID)
		}
		if err != nil {
			return err
		}

		day := restock.StartOfDay(at)
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM habit_logs WHERE habit_id = $1 AND done_at >= $2 AND done_at < $3)`,
			res.HabitID, day, day.AddDate(0, 0, 1)).Scan(&res.SameDay); err != nil {
			return err
		}
		if res.SameDay {
			return nil
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO habit_logs (habit_id, done_at) VALUES ($1, $2) RETURNING id`,
			res.HabitID, at).Scan(&res.LogID); err != nil {
			return err
		}
		return logAction(ctx, tx, chatID, updateID, "done", 0, 0, res.LogID)
	})
	return res, err
}

// Habits mengembalikan semua kegiatan milik chat beserta catatannya.
func (s *Store) Habits(ctx context.Context, chatID int64) ([]restock.Habit, error) {
	return s.loadHabits(ctx, chatID, 0)
}

// Habit mengembalikan satu kegiatan. ok=false kalau tidak ada atau milik chat lain.
func (s *Store) Habit(ctx context.Context, chatID, habitID int64) (restock.Habit, bool, error) {
	habits, err := s.loadHabits(ctx, chatID, habitID)
	if err != nil || len(habits) == 0 {
		return restock.Habit{}, false, err
	}
	return habits[0], true, nil
}

// HabitByLog mengembalikan kegiatan pemilik catatan tertentu.
func (s *Store) HabitByLog(ctx context.Context, chatID, logID int64) (restock.Habit, bool, error) {
	var habitID int64
	err := s.pool.QueryRow(ctx, `
		SELECT h.id FROM habit_logs l JOIN habits h ON h.id = l.habit_id
		WHERE l.id = $1 AND h.chat_id = $2`, logID, chatID).Scan(&habitID)
	if errors.Is(err, pgx.ErrNoRows) {
		return restock.Habit{}, false, nil
	}
	if err != nil {
		return restock.Habit{}, false, err
	}
	return s.Habit(ctx, chatID, habitID)
}

// loadHabits memuat kegiatan dan semua catatannya. habitID 0 berarti semua.
func (s *Store) loadHabits(ctx context.Context, chatID, habitID int64) ([]restock.Habit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, COALESCE(guess_days, 0)
		FROM habits WHERE chat_id = $1 AND ($2 = 0 OR id = $2)
		ORDER BY name`, chatID, habitID)
	if err != nil {
		return nil, err
	}
	habits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (restock.Habit, error) {
		var h restock.Habit
		err := row.Scan(&h.ID, &h.Name, &h.GuessDays)
		return h, err
	})
	if err != nil || len(habits) == 0 {
		return habits, err
	}
	byID := make(map[int64]*restock.Habit, len(habits))
	for i := range habits {
		byID[habits[i].ID] = &habits[i]
	}

	rows, err = s.pool.Query(ctx, `
		SELECT l.id, l.habit_id, l.done_at, l.reminded_at, l.snooze_until
		FROM habit_logs l JOIN habits h ON h.id = l.habit_id
		WHERE h.chat_id = $1 AND ($2 = 0 OR h.id = $2)
		ORDER BY l.done_at, l.id`, chatID, habitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l restock.HabitLog
		var habit int64
		var reminded, snooze *time.Time
		if err := rows.Scan(&l.ID, &habit, &l.DoneAt, &reminded, &snooze); err != nil {
			return nil, err
		}
		if reminded != nil {
			l.RemindedAt = *reminded
		}
		if snooze != nil {
			l.SnoozeUntil = *snooze
		}
		h := byID[habit]
		h.Logs = append(h.Logs, l)
	}
	return habits, rows.Err()
}

func (s *Store) SetHabitGuess(ctx context.Context, chatID, habitID int64, days int) (bool, error) {
	var guess *int
	if days > 0 {
		guess = &days
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE habits SET guess_days = $3 WHERE id = $1 AND chat_id = $2`, habitID, chatID, guess)
	return tag.RowsAffected() > 0, err
}

func (s *Store) MarkHabitReminded(ctx context.Context, logID int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE habit_logs SET reminded_at = $2 WHERE id = $1`, logID, at)
	return err
}

// SnoozeHabit menunda pengingat kegiatan sampai tanggal tertentu.
func (s *Store) SnoozeHabit(ctx context.Context, logID int64, until time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE habit_logs SET snooze_until = $2, reminded_at = NULL WHERE id = $1`, logID, until)
	return err
}
