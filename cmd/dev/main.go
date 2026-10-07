// Perintah dev menjalankan bot di laptop dengan long polling, tanpa perlu
// deploy. Pakai token bot terpisah: polling tidak bisa jalan selama bot
// yang sama masih terdaftar dengan webhook.
//
//	go run ./cmd/dev
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/ayndri/restok/app"
)

func main() {
	if err := app.LoadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}
	cfg, err := app.ConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	a, err := app.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	log.Println("Restok jalan (mode polling). Ctrl+C untuk berhenti.")
	var offset int64
	for ctx.Err() == nil {
		updates, err := a.Telegram.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if strings.Contains(err.Error(), "webhook") {
				log.Fatal("Bot ini masih terdaftar dengan webhook. Pakai token bot lain untuk development.")
			}
			log.Printf("getUpdates: %v (coba lagi 3 detik)", err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			switch {
			case u.Message != nil:
				log.Printf("pesan dari chat %d: %q", u.Message.Chat.ID, u.Message.Text)
			case u.CallbackQuery != nil:
				log.Printf("tombol ditekan: %q", u.CallbackQuery.Data)
			}
			if err := a.Bot.HandleUpdate(ctx, u); err != nil {
				log.Printf("update %d: %v", u.UpdateID, err)
			}
		}
	}
	log.Println("Berhenti.")
}
