package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/ayndri/restok/restock"
)

// RunDaily dipanggil cron tiap pagi. Setiap barang yang perkiraan habisnya
// sudah dekat dan belum punya cadangan dapat satu pesan dengan tombol.
// Unit yang kadaluwarsa dalam 14 hari dapat pesan terpisah, sekali saja.
// Satu chat atau satu barang gagal tidak menghentikan yang lain.
func (b *Bot) RunDaily(ctx context.Context) (int, error) {
	chats, err := b.Store.Chats(ctx)
	if err != nil {
		return 0, err
	}
	now := b.now()
	today := startOfDay(now)

	sent := 0
	var errs []error
	for _, chatID := range chats {
		if !b.allowed(chatID) {
			continue
		}
		items, err := b.Store.Items(ctx, chatID)
		if err != nil {
			errs = append(errs, fmt.Errorf("chat %d: %w", chatID, err))
			continue
		}
		for _, it := range items {
			if it.NeedsReminder(today) {
				err := b.Sender.SendMessage(ctx, chatID, formatReminder(it, today), reminderKeyboard(it.Active.ID))
				if err == nil {
					sent++
					err = b.Store.MarkReminded(ctx, it.Active.ID, now)
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("chat %d, %s: %w", chatID, it.Name, err))
				}
			}

			// Kadaluwarsa dicek per unit, termasuk cadangan yang belum dibuka.
			units := it.SpareUnits
			if it.Active != nil {
				units = append([]restock.Unit{*it.Active}, units...)
			}
			for _, u := range units {
				if !u.NeedsExpiryReminder(today) {
					continue
				}
				err := b.Sender.SendMessage(ctx, chatID, formatExpiryReminder(it, u, today), nil)
				if err == nil {
					sent++
					err = b.Store.MarkExpiryReminded(ctx, u.ID, now)
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("chat %d, %s kadaluwarsa: %w", chatID, it.Name, err))
				}
			}
		}
	}
	return sent, errors.Join(errs...)
}
