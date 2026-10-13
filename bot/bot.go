// Package bot menghubungkan pesan Telegram dengan pencatatan barang.
// Dipakai sama persis oleh webhook (Vercel) dan mode polling lokal.
package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"github.com/ayndri/restok/restock"
	"github.com/ayndri/restok/telegram"
)

type Store interface {
	Buy(ctx context.Context, chatID, updateID int64, name string, price int64, at time.Time) (restock.BuyResult, error)
	Finish(ctx context.Context, chatID, updateID, itemID int64, at time.Time) (restock.FinishResult, error)
	Undo(ctx context.Context, chatID int64) (restock.Undone, bool, error)
	Items(ctx context.Context, chatID int64) ([]restock.Item, error)
	Item(ctx context.Context, chatID, itemID int64) (restock.Item, bool, error)
	ItemByUnit(ctx context.Context, chatID, unitID int64) (restock.Item, bool, error)
	SetGuess(ctx context.Context, chatID, itemID int64, days int) (bool, error)
	MarkReminded(ctx context.Context, unitID int64, at time.Time) error
	Snooze(ctx context.Context, unitID int64, until time.Time) error
	Chats(ctx context.Context) ([]int64, error)
}

type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboardMarkup) error
	EditMessage(ctx context.Context, chatID, messageID int64, text string, kb *telegram.InlineKeyboardMarkup) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

type Bot struct {
	Store  Store
	Sender Sender
	// Allowed membatasi siapa yang boleh memakai bot. Kosong = semua orang.
	Allowed map[int64]bool
	// Now bisa diganti di tes. Nil = time.Now.
	Now func() time.Time
}

// Reply adalah balasan untuk satu pesan, boleh dengan tombol.
type Reply struct {
	Text     string
	Keyboard *telegram.InlineKeyboardMarkup
}

func text(s string) Reply { return Reply{Text: s} }

// Commands ditampilkan sebagai menu "/" di Telegram.
var Commands = []telegram.BotCommand{
	{Command: "stok", Description: "Semua barang dan perkiraan habisnya"},
	{Command: "batal", Description: "Batalkan catatan terakhir"},
	{Command: "bantuan", Description: "Cara pakai"},
}

func (b *Bot) now() time.Time {
	if b.Now != nil {
		return b.Now().In(restock.WIB)
	}
	return time.Now().In(restock.WIB)
}

func (b *Bot) allowed(chatID int64) bool {
	return len(b.Allowed) == 0 || b.Allowed[chatID]
}

func (b *Bot) HandleUpdate(ctx context.Context, u telegram.Update) error {
	switch {
	case u.CallbackQuery != nil:
		return b.handleCallback(ctx, u.UpdateID, u.CallbackQuery)
	case u.Message != nil && strings.TrimSpace(u.Message.Text) != "":
		return b.handleMessage(ctx, u.UpdateID, u.Message)
	}
	return nil
}

func (b *Bot) handleMessage(ctx context.Context, updateID int64, m *telegram.Message) error {
	chatID := m.Chat.ID
	if !b.allowed(chatID) {
		return b.Sender.SendMessage(ctx, chatID,
			fmt.Sprintf("Maaf, bot ini pribadi. ID chat kamu: <code>%d</code>", chatID), nil)
	}

	reply, err := b.respond(ctx, updateID, chatID, strings.TrimSpace(m.Text))
	if errors.Is(err, restock.ErrDuplicate) {
		return nil // sudah dibalas waktu kiriman pertama
	}
	if err != nil {
		log.Printf("update %d: %v", updateID, err)
		reply = text("Waduh, lagi ada gangguan. Coba kirim lagi sebentar ya.")
	}
	return b.Sender.SendMessage(ctx, chatID, reply.Text, reply.Keyboard)
}

func (b *Bot) respond(ctx context.Context, updateID, chatID int64, msg string) (Reply, error) {
	if !strings.HasPrefix(msg, "/") {
		return b.record(ctx, updateID, chatID, msg)
	}

	switch command(strings.Fields(msg)[0]) {
	case "/start", "/bantuan", "/help":
		return text(helpText), nil
	case "/stok":
		items, err := b.Store.Items(ctx, chatID)
		if err != nil {
			return Reply{}, err
		}
		return text(formatStock(items, startOfDay(b.now()))), nil
	case "/batal":
		return b.undo(ctx, chatID)
	}
	return text("Perintah itu belum ada. Ketik /bantuan buat lihat daftarnya."), nil
}

// command mengubah "/Stok@restok_bot" menjadi "/stok".
func command(word string) string {
	cmd, _, _ := strings.Cut(word, "@")
	return strings.ToLower(cmd)
}

func (b *Bot) record(ctx context.Context, updateID, chatID int64, msg string) (Reply, error) {
	day, rest, err := restock.SplitDate(msg, startOfDay(b.now()))
	if errors.Is(err, restock.ErrFutureDate) {
		return text("Tanggalnya belum lewat 🙂 Buat catatan yang kelupaan, tulis tanggal yang udah lewat, misalnya <code>kemarin beli sunscreen</code>."), nil
	}
	at := b.at(day)

	cmd, err := restock.Parse(rest)
	switch {
	case errors.Is(err, restock.ErrUnknown):
		return text(parseHint), nil
	case errors.Is(err, restock.ErrNoName):
		return text("Nama barangnya apa? Contoh: <code>beli sunscreen 89rb</code> atau <code>habis sunscreen</code>."), nil
	case errors.Is(err, restock.ErrNameTooLong):
		return text(fmt.Sprintf("Nama barangnya kepanjangan, maksimal %d huruf.", restock.MaxNameLen)), nil
	case err != nil:
		return Reply{}, err
	}

	if cmd.Action == restock.Buy {
		return b.buy(ctx, updateID, chatID, cmd, at)
	}

	items, err := b.Store.Items(ctx, chatID)
	if err != nil {
		return Reply{}, err
	}
	matches := restock.Match(items, cmd.Name)
	switch len(matches) {
	case 0:
		return text(fmt.Sprintf("Belum ada <b>%s</b> di catatan.\nCatat dulu waktu beli: <code>beli %s</code>",
			html.EscapeString(restock.Display(cmd.Name)), html.EscapeString(cmd.Name))), nil
	case 1:
		s, err := b.finish(ctx, updateID, chatID, matches[0], at)
		return text(s), err
	}
	return Reply{Text: "Maksud kamu yang mana?", Keyboard: pickItemKeyboard(matches, day)}, nil
}

// at mengubah tanggal hasil SplitDate menjadi waktu catatan. Catatan
// bertanggal mundur memakai jam yang sama dengan sekarang, seperti Dompetku.
func (b *Bot) at(day time.Time) time.Time {
	now := b.now()
	if day.IsZero() {
		return now
	}
	return day.Add(now.Sub(startOfDay(now)))
}

// backdated menambahkan baris tanggal kalau catatan bukan untuk hari ini.
func (b *Bot) backdated(at time.Time) string {
	if startOfDay(at).Equal(startOfDay(b.now())) {
		return ""
	}
	return "\n📅 Dicatat untuk tanggal " + shortDate(at)
}

func (b *Bot) buy(ctx context.Context, updateID, chatID int64, cmd restock.Command, at time.Time) (Reply, error) {
	res, err := b.Store.Buy(ctx, chatID, updateID, cmd.Name, cmd.Price, at)
	if err != nil {
		return Reply{}, err
	}
	it, _, err := b.Store.Item(ctx, chatID, res.ItemID)
	if err != nil {
		return Reply{}, err
	}

	s := formatBought(it, cmd.Price, res.Spare) + b.backdated(at)
	if res.NewItem {
		return Reply{Text: s + "\n\nIni pertama kali kamu catat. Biasanya habis berapa lama?", Keyboard: guessKeyboard(it.ID)}, nil
	}
	return text(s + "\n\nSalah ketik? /batal"), nil
}

// finish dipakai oleh pesan "habis ..." maupun tombol.
func (b *Bot) finish(ctx context.Context, updateID, chatID int64, it restock.Item, at time.Time) (string, error) {
	// Habis sebelum mulai dipakai pasti salah ketik, dan kalau dibiarkan
	// siklusnya tercatat 1 hari lalu merusak perkiraan.
	if it.Active != nil && startOfDay(at).Before(startOfDay(it.Active.OpenedAt)) {
		return fmt.Sprintf("%s baru mulai dipakai tanggal %s, jadi nggak mungkin habis tanggal %s. Cek lagi tanggalnya ya.",
			bold(it.Name), shortDate(it.Active.OpenedAt), shortDate(at)), nil
	}
	res, err := b.Store.Finish(ctx, chatID, updateID, it.ID, at)
	if err != nil {
		return "", err
	}
	name := html.EscapeString(restock.Display(it.Name))
	if res.NothingActive {
		return fmt.Sprintf("<b>%s</b> udah tercatat habis.\nKalau udah beli lagi, ketik <code>beli %s</code>.",
			name, html.EscapeString(it.Name)), nil
	}
	after, _, err := b.Store.Item(ctx, chatID, it.ID)
	if err != nil {
		return "", err
	}
	return formatFinished(after, res, b.backdated(at)), nil
}

func (b *Bot) undo(ctx context.Context, chatID int64) (Reply, error) {
	u, ok, err := b.Store.Undo(ctx, chatID)
	if err != nil {
		return Reply{}, err
	}
	if !ok {
		return text("Belum ada catatan yang bisa dibatalkan."), nil
	}
	name := html.EscapeString(restock.Display(u.Name))
	if u.Action == restock.Buy {
		return text(fmt.Sprintf("↩️ Dibatalkan: beli <b>%s</b>.", name)), nil
	}
	return text(fmt.Sprintf("↩️ Dibatalkan: <b>%s</b> habis. Barangnya dianggap masih dipakai.", name)), nil
}

func startOfDay(t time.Time) time.Time { return restock.StartOfDay(t) }
