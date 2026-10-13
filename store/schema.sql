-- Jenis barang milik satu chat.
CREATE TABLE IF NOT EXISTS items (
    id            BIGSERIAL PRIMARY KEY,
    chat_id       BIGINT      NOT NULL,
    name          TEXT        NOT NULL, -- sudah dinormalisasi, huruf kecil
    -- Tebakan awal dari tombol, dipakai sebelum ada riwayat. NULL = belum ada.
    guess_days    INT         CHECK (guess_days > 0),
    remind_before INT         NOT NULL DEFAULT 5,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, name)
);

-- Satu baris = satu barang fisik. Status diturunkan dari kolom:
-- opened_at NULL = cadangan, finished_at NULL = sedang dipakai, selain itu habis.
CREATE TABLE IF NOT EXISTS units (
    id           BIGSERIAL PRIMARY KEY,
    item_id      BIGINT      NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    price        BIGINT      NOT NULL DEFAULT 0 CHECK (price >= 0),
    bought_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    opened_at    TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    -- Supaya pengingat untuk unit yang sama tidak dikirim tiap pagi.
    reminded_at  TIMESTAMPTZ,
    snooze_until TIMESTAMPTZ
);

-- Ditambahkan belakangan; ALTER supaya database lama ikut punya kolomnya.
-- expires_on NULL = tanggal kadaluwarsa tidak dicatat.
ALTER TABLE units ADD COLUMN IF NOT EXISTS expires_on DATE;
ALTER TABLE units ADD COLUMN IF NOT EXISTS expiry_reminded_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS units_item_idx ON units (item_id);

-- Satu barang hanya boleh punya satu unit yang sedang dipakai.
CREATE UNIQUE INDEX IF NOT EXISTS units_one_active_idx
    ON units (item_id) WHERE opened_at IS NOT NULL AND finished_at IS NULL;

-- Log aksi untuk /batal, sekaligus penolak kiriman ulang dari Telegram:
-- satu update_id hanya boleh menghasilkan satu aksi.
CREATE TABLE IF NOT EXISTS actions (
    id             BIGSERIAL PRIMARY KEY,
    chat_id        BIGINT      NOT NULL,
    update_id      BIGINT      NOT NULL,
    kind           TEXT        NOT NULL CHECK (kind IN ('buy', 'finish')),
    unit_id        BIGINT      NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    -- Cadangan yang otomatis dibuka saat unit_id habis; dikembalikan saat /batal.
    opened_unit_id BIGINT      REFERENCES units (id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, update_id)
);

-- Setelah tombol "Atur kadaluwarsa" ditekan, pesan berikutnya dibaca sebagai
-- tanggal untuk unit ini. Satu chat hanya menunggu satu unit.
CREATE TABLE IF NOT EXISTS pending_expiry (
    chat_id    BIGINT      PRIMARY KEY,
    unit_id    BIGINT      NOT NULL REFERENCES units (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Kegiatan rutin ("terakhir kali"): potong rambut, ganti sprei, servis AC.
CREATE TABLE IF NOT EXISTS habits (
    id         BIGSERIAL PRIMARY KEY,
    chat_id    BIGINT      NOT NULL,
    name       TEXT        NOT NULL, -- sudah dinormalisasi, huruf kecil
    guess_days INT         CHECK (guess_days > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (chat_id, name)
);

-- Satu baris = satu kali kegiatan dilakukan. Status pengingat ada di catatan
-- terakhir, sama seperti unit, supaya /batal ikut mengembalikannya.
CREATE TABLE IF NOT EXISTS habit_logs (
    id           BIGSERIAL PRIMARY KEY,
    habit_id     BIGINT      NOT NULL REFERENCES habits (id) ON DELETE CASCADE,
    done_at      TIMESTAMPTZ NOT NULL,
    reminded_at  TIMESTAMPTZ,
    snooze_until TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS habit_logs_habit_idx ON habit_logs (habit_id, done_at);

-- Aksi "done" menunjuk ke catatan kegiatan, bukan unit. Constraint dibuang lalu
-- dibuat ulang supaya database lama dan baru berakhir dengan aturan yang sama.
ALTER TABLE actions ADD COLUMN IF NOT EXISTS log_id BIGINT REFERENCES habit_logs (id) ON DELETE CASCADE;
ALTER TABLE actions ALTER COLUMN unit_id DROP NOT NULL;
ALTER TABLE actions DROP CONSTRAINT IF EXISTS actions_kind_check;
ALTER TABLE actions ADD CONSTRAINT actions_kind_check CHECK (kind IN ('buy', 'finish', 'done'));
ALTER TABLE actions DROP CONSTRAINT IF EXISTS actions_target_check;
ALTER TABLE actions ADD CONSTRAINT actions_target_check CHECK ((unit_id IS NULL) <> (log_id IS NULL));
