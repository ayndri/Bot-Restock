# Restok

**Bot Telegram pencatat barang pribadi** (skincare, toiletries, kebutuhan rutin).
Catat waktu beli dan waktu habis, bot belajar seberapa cepat kamu ngabisin tiap
barang, lalu mengingatkan **sebelum** barangnya habis.

Ditulis dengan **Go**, jalan sebagai fungsi serverless di **Vercel**, data di
**Neon Postgres**. Kerangkanya sama dengan [Dompetku](https://github.com/ayndri/Bot-Dompet).

```
kamu: beli sunscreen 89rb
bot : 🛒 Sunscreen dicatat · Rp89.000
      Ini pertama kali kamu catat. Biasanya habis berapa lama?
      [2 minggu] [1 bulan] [2 bulan] [3 bulan] [Nggak tahu]

... 36 hari kemudian, jam 8 pagi ...
bot : 🔔 Sunscreen kemungkinan habis sekitar 5 hari lagi (11 Okt).
      Udah beli lagi?
      [🛒 Udah beli] [⏳ Masih ada] [✅ Udah habis]
```

## Fitur

- **Dua kata kerja saja.** `beli sunscreen 89rb` dan `habis sunscreen`. Harga boleh
  ditulis di mana saja dan paham `89rb`, `1,5jt`, `Rp45.000`. Angka polos di bawah
  seribu dianggap bagian dari nama (`sunscreen spf 50`).
- **Siklus dipelajari sendiri.** Lama pemakaian tiap unit dicatat. Perkiraan berikutnya
  memakai median dari siklus terbaru. Siklus yang janggal (lebih dari 2× atau kurang
  dari separuh median, biasanya karena lupa lapor) dibuang supaya perkiraan tidak rusak.
- **Tebakan awal.** Sebelum ada riwayat, bot bertanya sekali lewat tombol.
- **Cadangan.** Beli lagi padahal yang lama belum habis → disimpan sebagai cadangan.
  Saat yang lama habis, cadangan otomatis mulai dipakai. Barang yang punya cadangan
  tidak diingatkan.
- **Pengingat pagi.** Satu Vercel Cron tiap 08.00 WIB, 5 hari sebelum perkiraan habis.
  Tombol *Masih ada* menunda 3 hari. Pengingat yang sama tidak dikirim tiap hari.
- **`/stok`** urut dari yang paling cepat habis. **`/batal`** membatalkan aksi
  terakhir, termasuk mengembalikan cadangan yang ikut terbuka.
- **Tidak dobel.** Kiriman ulang dari Telegram ditolak lewat `UNIQUE (chat_id, update_id)`.
- **Bisa dibuat pribadi** lewat `ALLOWED_CHAT_IDS`.

## Arsitektur

| Folder | Isi |
|---|---|
| `restock/` | Aturan inti tanpa I/O: membaca pesan, prediksi siklus, kapan mengingatkan. |
| `bot/` | Logika percakapan dan tombol. Bergantung pada interface `Store` dan `Sender`, jadi bisa dites tanpa database dan tanpa Telegram. |
| `store/` | Query Postgres dengan `pgx`. Skema di `store/schema.sql`. |
| `telegram/` | Klien Bot API kecil, tanpa library pihak ketiga. |
| `api/` | Fungsi serverless Vercel: `webhook.go` untuk pesan Telegram, `cron.go` untuk pengingat pagi. |
| `cmd/` | `dev` (jalan lokal), `migrate` (buat tabel), `setwebhook` (daftarkan URL). |

## Menjalankan di laptop

Butuh Go 1.25+, akun Neon, dan bot dari [@BotFather](https://t.me/BotFather).
Untuk development, bikin **bot kedua** khusus lokal.

```bash
cp .env.example .env        # isi token bot lokal dan DATABASE_URL
go run ./cmd/migrate        # buat tabel
go run ./cmd/dev            # bot jalan, coba chat dia di Telegram
go test ./...
```

Tes `store/` butuh Postgres sungguhan dan dilewati kalau `TEST_DATABASE_URL` kosong.
Isi tabelnya dihapus setiap tes jalan, jadi **jangan** arahkan ke database produksi:

```bash
TEST_DATABASE_URL="postgres://postgres@localhost:5432/restok_test?sslmode=disable" go test ./store/
```

## Deploy ke Vercel

1. Push repo ke GitHub, lalu import di Vercel (framework: **Other**).
2. Isi Environment Variables: `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`,
   `CRON_SECRET`, `DATABASE_URL`, dan opsional `ALLOWED_CHAT_IDS`.
3. Setelah deploy, daftarkan webhook dari laptop (pakai token bot produksi):

   ```bash
   go run ./cmd/setwebhook -url https://<nama-project>.vercel.app
   ```

Rencana berikutnya ada di [ROADMAP.md](ROADMAP.md).
