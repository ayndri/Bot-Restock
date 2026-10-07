# 🧴 Restok — Roadmap Project

> Bot Telegram pencatat barang pribadi (skincare, toiletries, kebutuhan rutin).
> Catat kapan dibeli dan kapan habis → bot belajar siklus pemakaianmu sendiri →
> mengingatkan **sebelum** barangnya habis atau kedaluwarsa.

---

## 1. Konsep Inti

Dua kata kerja utama, sisanya otomatis:

```
kamu: beli sunscreen 89rb
bot : Dicatat ✓ Sunscreen — 89rb. Ini pertama kali, biasanya habis berapa lama?
      [2 minggu] [1 bulan] [2 bulan] [nggak tahu]

... 38 hari kemudian, cron pagi ...
bot : Sunscreen kemungkinan habis ±3 hari lagi. Beli lagi?
      [udah beli] [masih ada] [udah habis]

kamu: habis sunscreen
bot : Sunscreen habis setelah 41 hari. Rata-rata kamu: 40 hari.
```

Yang bikin beda dari aplikasi pengingat biasa:

1. **Siklus dipelajari, bukan diisi manual.** Jarak "mulai dipakai" → "habis" dicatat
   tiap unit; prediksi berikutnya dari riwayat itu.
2. **Dua jenis batas waktu.** *Habis* (dipakai sampai habis) dan *basi* (masa pakai
   setelah dibuka, mis. serum vit C 6 bulan, sikat gigi 3 bulan). Mana yang duluan,
   itu yang diingatkan.
3. **Tahu kalau kamu lupa lapor.** Lewat tanggal prediksi + 7 hari tanpa kabar →
   bot bertanya, bukan diam-diam bikin data jadi salah.

---

## 2. Tech Stack (sama dengan Dompetku)

| Bagian | Pilihan | Catatan |
|---|---|---|
| Bahasa | **Go** | Struktur folder ikut Dompetku |
| Hosting | **Vercel** (serverless Go) | `api/webhook.go` + `api/cron.go` |
| Database | **Neon Postgres** + `pgx` | |
| Telegram | Klien kecil sendiri | Salin `telegram/` dari Dompetku |
| Jadwal | **Vercel Cron**, 1× sehari 08.00 WIB | Ketelitian per hari sudah cukup |

**Dipakai ulang dari Dompetku:** `telegram/`, parser rupiah (`ledger/rupiah.go`,
bagian nominal di `ledger/parse.go`), anti-dobel `update_id`, `ALLOWED_CHAT_IDS`,
pola `cmd/dev`, `cmd/migrate`, `cmd/setwebhook`.

---

## 3. Model Data

Kuncinya: **satu baris per unit fisik**, bukan per jenis barang. Beli 2 sabun = 2 unit.
Siklus = `finished_at − opened_at` per unit. Skema lengkap di `store/schema.sql`.

| Tabel | Isi |
|---|---|
| `items` | Jenis barang per chat: nama (dinormalisasi), `guess_days`, `remind_before` |
| `units` | Satu barang fisik: harga, `bought_at`, `opened_at`, `finished_at`, `reminded_at`, `snooze_until` |
| `actions` | Log `buy` / `finish` untuk `/batal`, sekaligus penolak kiriman ulang (`UNIQUE (chat_id, update_id)`) |

Status unit diturunkan dari kolom, tidak disimpan terpisah:
**cadangan** (`opened_at` NULL) → **dipakai** → **habis** (`finished_at` terisi).

Aturan otomatis:
- Beli saat tidak ada yang dipakai → unit langsung **dipakai**. Kalau masih ada → jadi **cadangan**.
- Saat unit yang dipakai habis dan ada cadangan → cadangan tertua otomatis dibuka.
- Satu barang hanya boleh punya **satu unit dipakai** sekaligus (dijaga unique index parsial).
- `/batal` hanya membatalkan aksi terakhir. Aksi `finish` menyimpan cadangan yang ikut
  dibuka, jadi pembatalannya mengembalikan dua-duanya.

---

## 4. Prediksi

Pure function di paket tanpa I/O (`restock/`), wajib dites.

1. Ambil maksimal **5 siklus terakhir** yang sudah habis, cari mediannya.
2. Siklus yang < 0,5× atau > 2× median dibuang: biasanya karena lupa lapor "habis"
   (siklus jadi 2× lipat) atau salah ketik.
3. Hasil = **median dari 3 siklus terbaru** yang tersisa.
4. Belum ada riwayat → pakai `guess_days`. Tidak ada juga → barang itu tidak diprediksi,
   cukup dicatat.
5. `perkiraan_habis = opened_at + median`.
   `perkiraan_basi = opened_at + shelf_life_days`.
   Tanggal pengingat = yang lebih dulu, dikurangi `remind_before`.

---

## 5. Perintah

| Pesan | Arti |
|---|---|
| `beli sunscreen 89rb` | Catat 1 unit. Harga opsional. |
| `habis sunscreen` | Tutup unit yang sedang dipakai. Cocok sebagian juga bisa (`habis cuci muka`). |
| `/stok` | Semua barang: perkiraan habis, cadangan, yang sudah habis. |
| `/batal` | Batalkan aksi terakhir. |
| `beli 2 sabun muka 45rb` | *(v1)* 2 unit; harga dianggap total → per unit 22.500. |
| `buka serum` | *(v1)* Mulai pakai cadangan (penting untuk barang yang punya masa basi). |
| `serum tahan 6 bulan` | *(v1)* Atur masa pakai setelah dibuka. |
| `/belanja` | *(v1)* Barang yang diperkirakan habis ≤ 7 hari dan tidak punya cadangan. |
| `/riwayat sunscreen` | *(v1)* Daftar siklus + harga per unit. |
| `/arsip sunscreen` | *(v1)* Berhenti melacak barang ini. |

Kalau `habis …` cocok ke beberapa barang (`habis sabun`), bot menampilkan tombol pilihan.

*(v1)* Nama barang yang belum dikenal tapi mirip yang sudah ada saat `beli` (mis.
`sabun muka` vs `sabun cuci muka`) → bot bertanya dengan tombol **[sama]** /
**[barang baru]**. Jangan menebak diam-diam. Di MVP, `beli` hanya mencocokkan nama persis.

---

## 6. Fitur — Bertahap

### 🟢 MVP
- [x] Salin kerangka Dompetku: `telegram/`, `cmd/`, `api/webhook.go`, parser rupiah
- [x] Skema `items` + `units` + `actions`, `cmd/migrate`
- [x] `beli <nama> [harga]`, `habis <nama>`, `/stok`, `/batal`
- [x] Tombol tebakan awal di pembelian pertama
- [x] Prediksi median + tes unit
- [x] Cron 08.00 WIB: pengingat "mau habis" dengan tombol [udah beli] [masih ada] [udah habis]
- [x] Cadangan otomatis dibuka saat yang dipakai habis
- [x] Tombol tunda: "masih ada" → tanya lagi 3 hari kemudian
- [ ] Bikin bot di BotFather + database Neon, deploy ke Vercel
- [ ] Pakai sendiri seminggu, catat apa yang janggal

### 🟡 v1 (bot utuh)
- [ ] Jumlah unit (`beli 2 …`)
- [ ] Masa basi: `buka`, `tahan N bulan`, pengingat "mau basi"
- [ ] Tanya kalau lupa lapor (lewat prediksi + 7 hari)
- [ ] `/belanja` dan `/riwayat`
- [ ] Pencocokan nama mirip saat `beli`, dengan tombol konfirmasi
- [ ] `/arsip` untuk berhenti melacak barang

### 🔵 v2 (nilai plus)
- [ ] Riwayat harga: "terakhir 45rb, sekarang 52rb"
- [ ] Ringkasan bulanan: total belanja per barang, barang paling cepat habis
- [ ] Grafik siklus per barang dikirim sebagai gambar
- [ ] README dengan screenshot percakapan (seperti Dompetku)

---

## 7. Yang Sengaja Tidak Dikerjakan

- **Integrasi otomatis ke Dompetku.** Menarik, tapi dua bot jadi saling bergantung.
  Kalau nanti mau, cukup tombol "catat juga ke Dompetku" — bukan berbagi database.
- **Scan barcode / foto produk.** Ribet dan tidak menambah inti fitur.
- **Banyak pengguna dalam satu grup.** Bot ini pribadi; `chat_id` = satu orang.

---

## 8. Keputusan yang Masih Terbuka

- **Nama bot.** "Restok" sementara; ganti sebelum daftar ke BotFather kalau ada yang lebih pas.
- **Barang yang dipakai paralel** (mis. sabun di dua kamar mandi). MVP: tidak didukung,
  dicatat sebagai dua barang berbeda. Lihat lagi setelah dipakai sebulan.
