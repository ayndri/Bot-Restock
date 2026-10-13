package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/ayndri/restok/restock"
	"github.com/ayndri/restok/telegram"
)

// Data tombol (Telegram membatasi 64 byte):
//
//	g:<item>:<hari>  tebakan awal lama pemakaian
//	f:<item>[:<tgl>] pilih barang saat "habis ..." cocok ke beberapa barang;
//	                 tgl (YYYYMMDD) ada kalau pesannya bertanggal mundur
//	e:<unit>         atur tanggal kadaluwarsa; pesan berikutnya dibaca sebagai tanggal
//	rb:<unit>        pengingat → udah beli
//	rm:<unit>        pengingat → masih ada (tunda)
//	rh:<unit>        pengingat → udah habis
//
// Tombol pengingat memakai id unit, bukan id barang, supaya tombol lama
// tidak berlaku untuk unit berikutnya.

var guessOptions = []struct {
	label string
	days  int
}{
	{"2 minggu", 14}, {"1 bulan", 30}, {"2 bulan", 60}, {"3 bulan", 90}, {"Nggak tahu", 0},
}

func button(label, data string) telegram.InlineKeyboardButton {
	return telegram.InlineKeyboardButton{Text: label, CallbackData: data}
}

func expiryButton(unitID int64) []telegram.InlineKeyboardButton {
	return []telegram.InlineKeyboardButton{button("📅 Atur kadaluwarsa", fmt.Sprintf("e:%d", unitID))}
}

func expiryKeyboard(unitID int64) *telegram.InlineKeyboardMarkup {
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{expiryButton(unitID)}}
}

// guessKeyboard menambahkan tombol kadaluwarsa di bawah kalau unitID tidak 0.
func guessKeyboard(itemID, unitID int64) *telegram.InlineKeyboardMarkup {
	var rows [][]telegram.InlineKeyboardButton
	var row []telegram.InlineKeyboardButton
	for _, o := range guessOptions {
		row = append(row, button(o.label, fmt.Sprintf("g:%d:%d", itemID, o.days)))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	if unitID != 0 {
		rows = append(rows, expiryButton(unitID))
	}
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// maxPick membatasi tombol pilihan supaya pesannya tidak memanjang.
const maxPick = 6

// pickDateLayout adalah format tanggal di tombol "f:<item>:<tanggal>".
const pickDateLayout = "20060102"

// pickItemKeyboard ikut membawa tanggal dari "kemarin habis sabun", supaya
// barang yang dipilih tetap dicatat habis di tanggal itu. day nol = hari ini.
func pickItemKeyboard(items []restock.Item, day time.Time) *telegram.InlineKeyboardMarkup {
	var rows [][]telegram.InlineKeyboardButton
	for i, it := range items {
		if i == maxPick {
			break
		}
		data := fmt.Sprintf("f:%d", it.ID)
		if !day.IsZero() {
			data += ":" + day.Format(pickDateLayout)
		}
		rows = append(rows, []telegram.InlineKeyboardButton{button(restock.Display(it.Name), data)})
	}
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func reminderKeyboard(unitID int64) *telegram.InlineKeyboardMarkup {
	return &telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{{
		button("🛒 Udah beli", fmt.Sprintf("rb:%d", unitID)),
		button("⏳ Masih ada", fmt.Sprintf("rm:%d", unitID)),
	}, {
		button("✅ Udah habis", fmt.Sprintf("rh:%d", unitID)),
	}}}
}

// snoozeDays adalah jeda sebelum bertanya lagi setelah "Masih ada".
const snoozeDays = 3

func (b *Bot) handleCallback(ctx context.Context, updateID int64, q *telegram.CallbackQuery) error {
	if q.Message == nil {
		return b.Sender.AnswerCallback(ctx, q.ID, "")
	}
	chatID := q.Message.Chat.ID
	if !b.allowed(chatID) {
		return b.Sender.AnswerCallback(ctx, q.ID, "Bot ini pribadi.")
	}

	notice, err := b.callback(ctx, updateID, chatID, q.Message.MessageID, q.Data)
	if errors.Is(err, restock.ErrDuplicate) {
		err = nil
	}
	if err != nil {
		log.Printf("tombol %q di chat %d: %v", q.Data, chatID, err)
		notice = "Lagi ada gangguan, coba lagi sebentar ya."
	}
	// Tombol harus selalu dijawab, kalau tidak ikon loading-nya berputar terus.
	return b.Sender.AnswerCallback(ctx, q.ID, notice)
}

func (b *Bot) callback(ctx context.Context, updateID, chatID, messageID int64, data string) (string, error) {
	action, rest, _ := strings.Cut(data, ":")
	idPart, arg, _ := strings.Cut(rest, ":")
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil {
		return "", nil // data rusak; abaikan
	}
	editKB := func(s string, kb *telegram.InlineKeyboardMarkup) error {
		return b.Sender.EditMessage(ctx, chatID, messageID, s, kb)
	}
	edit := func(s string) error { return editKB(s, nil) }

	switch action {
	case "g":
		days, err := strconv.Atoi(arg)
		if err != nil || days < 0 {
			return "", nil
		}
		ok, err := b.Store.SetGuess(ctx, chatID, id, days)
		if err != nil || !ok {
			return "", err
		}
		it, _, err := b.Store.Item(ctx, chatID, id)
		if err != nil {
			return "", err
		}
		// Tombol kadaluwarsa tetap ada setelah tebakan dijawab, selama belum diisi.
		var kb *telegram.InlineKeyboardMarkup
		if it.Active != nil && it.Active.ExpiresOn.IsZero() {
			kb = expiryKeyboard(it.Active.ID)
		}
		return "", editKB(formatGuessSet(it, days), kb)

	case "e":
		it, ok, err := b.Store.ItemByUnit(ctx, chatID, id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "Catatan belinya udah dibatalkan.", nil
		}
		if err := b.Store.SetPending(ctx, chatID, id, b.now()); err != nil {
			return "", err
		}
		return "", b.Sender.SendMessage(ctx, chatID, fmt.Sprintf(
			"📅 Kapan kadaluwarsanya %s? Ketik tanggal di kemasan, misalnya <code>8/2027</code> atau <code>15/8/2027</code>.\nNggak jadi? Abaikan aja.",
			bold(it.Name)), nil)

	case "f":
		it, ok, err := b.Store.Item(ctx, chatID, id)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", edit("<i>Barang ini udah nggak ada di catatan.</i>")
		}
		var day time.Time
		if arg != "" {
			if day, err = time.ParseInLocation(pickDateLayout, arg, restock.WIB); err != nil {
				return "", nil
			}
		}
		s, err := b.finish(ctx, updateID, chatID, it, b.at(day))
		if err != nil {
			return "", err
		}
		return "", edit(s)

	case "rb", "rm", "rh":
		return b.reminderAnswer(ctx, updateID, chatID, id, action, editKB)
	}
	return "", nil
}

func (b *Bot) reminderAnswer(ctx context.Context, updateID, chatID, unitID int64, action string,
	editKB func(string, *telegram.InlineKeyboardMarkup) error) (string, error) {
	edit := func(s string) error { return editKB(s, nil) }
	it, ok, err := b.Store.ItemByUnit(ctx, chatID, unitID)
	if err != nil {
		return "", err
	}
	// Unitnya sudah habis atau dibatalkan sejak pengingat dikirim.
	if !ok || it.Active == nil || it.Active.ID != unitID {
		return "Pengingat ini udah nggak berlaku.", edit("<i>Pengingat ini udah nggak berlaku.</i>")
	}
	name := bold(it.Name)

	switch action {
	case "rb":
		if it.Spares > 0 {
			return "Udah kecatat ada cadangan.", edit(fmt.Sprintf("📦 %s udah punya cadangan, aman.", name))
		}
		res, err := b.Store.Buy(ctx, chatID, updateID, it.Name, 0, b.now(), time.Time{})
		if err != nil {
			return "", err
		}
		return "Dicatat 🛒", editKB(fmt.Sprintf(
			"📦 Cadangan %s dicatat. Begitu yang lama habis, ketik <code>habis %s</code>.",
			name, html.EscapeString(it.Name)), expiryKeyboard(res.UnitID))

	case "rm":
		until := startOfDay(b.now()).AddDate(0, 0, snoozeDays)
		if err := b.Store.Snooze(ctx, unitID, until); err != nil {
			return "", err
		}
		return "", edit(fmt.Sprintf("⏳ Oke, %s masih ada. Aku tanya lagi %d hari lagi.", name, snoozeDays))

	default: // "rh"
		s, err := b.finish(ctx, updateID, chatID, it, b.now())
		if err != nil {
			return "", err
		}
		return "", edit(s)
	}
}
