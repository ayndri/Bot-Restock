// Perintah setwebhook memberi tahu Telegram alamat bot di Vercel, sekaligus
// memasang menu perintah. Cukup dijalankan sekali setelah deploy pertama.
//
//	go run ./cmd/setwebhook -url https://restok.vercel.app
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/ayndri/restok/app"
	"github.com/ayndri/restok/bot"
	"github.com/ayndri/restok/telegram"
)

func main() {
	baseURL := flag.String("url", "", "alamat deploy Vercel, misalnya https://restok.vercel.app")
	flag.Parse()
	if *baseURL == "" {
		flag.Usage()
		os.Exit(2)
	}

	if err := app.LoadDotEnv(".env"); err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	secret := os.Getenv("TELEGRAM_WEBHOOK_SECRET")
	if token == "" || secret == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN dan TELEGRAM_WEBHOOK_SECRET wajib diisi")
	}

	ctx := context.Background()
	tg := telegram.New(token)
	hook := strings.TrimRight(*baseURL, "/") + "/api/webhook"
	if err := tg.SetWebhook(ctx, hook, secret); err != nil {
		log.Fatal(err)
	}
	if err := tg.SetMyCommands(ctx, bot.Commands); err != nil {
		log.Fatal(err)
	}
	log.Printf("Webhook terpasang di %s", hook)
}
