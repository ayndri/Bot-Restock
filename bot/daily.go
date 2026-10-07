package bot

import (
	"context"
	"errors"
	"fmt"
)

// RunDaily dipanggil cron tiap pagi. Setiap barang yang perkiraan habisnya
// sudah dekat dan belum punya cadangan dapat satu pesan dengan tombol.
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
			if !it.NeedsReminder(today) {
				continue
			}
			err := b.Sender.SendMessage(ctx, chatID, formatReminder(it, today), reminderKeyboard(it.Active.ID))
			if err == nil {
				sent++
				err = b.Store.MarkReminded(ctx, it.Active.ID, now)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("chat %d, %s: %w", chatID, it.Name, err))
			}
		}
	}
	return sent, errors.Join(errs...)
}
