package handler

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ayndri/restok/app"
)

// Cron melayani /api/cron, dipanggil Vercel Cron tiap pagi pukul 08.00 WIB
// (jadwal di vercel.json) untuk mengirim pengingat barang yang hampir habis.
func Cron(w http.ResponseWriter, r *http.Request) {
	a, err := app.Default()
	if err != nil {
		log.Printf("init: %v", err)
		http.Error(w, "server belum siap", http.StatusInternalServerError)
		return
	}
	secret := a.Config.CronSecret
	if secret == "" {
		log.Print("CRON_SECRET belum diisi")
		http.Error(w, "server belum siap", http.StatusInternalServerError)
		return
	}

	// Vercel menyertakan CRON_SECRET di header ini. Tanpa cek, siapa pun bisa
	// memicu kiriman pesan berkali-kali.
	got := r.Header.Get("Authorization")
	if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	sent, err := a.Bot.RunDaily(ctx)
	if err != nil {
		log.Printf("cron harian: %v", err)
		http.Error(w, fmt.Sprintf("terkirim ke %d chat, sebagian gagal", sent), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "terkirim ke %d chat\n", sent)
}
