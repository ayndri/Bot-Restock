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
