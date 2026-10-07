// Package handler berisi fungsi serverless Vercel. File ini melayani
// /api/webhook, yang dipanggil Telegram setiap ada pesan masuk ke bot.
package handler

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/ayndri/restok/app"
	"github.com/ayndri/restok/telegram"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	a, err := app.Default()
	if err != nil {
		log.Printf("init: %v", err)
		http.Error(w, "server belum siap", http.StatusInternalServerError)
		return
	}
	secret := a.Config.WebhookSecret
	if secret == "" {
		log.Print("TELEGRAM_WEBHOOK_SECRET belum diisi")
		http.Error(w, "server belum siap", http.StatusInternalServerError)
		return
	}

	// Tanpa cek ini, siapa pun yang tahu URL-nya bisa menulis ke database.
	got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var u telegram.Update
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&u); err != nil {
		// Tetap 200: kalau dibalas error, Telegram akan terus mengirim ulang.
		log.Printf("update tidak terbaca: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
	defer cancel()
	if err := a.Bot.HandleUpdate(ctx, u); err != nil {
		log.Printf("update %d: %v", u.UpdateID, err)
	}
	w.WriteHeader(http.StatusOK)
}
