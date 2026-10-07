// Package telegram adalah klien kecil untuk Telegram Bot API, cukup untuk
// kebutuhan bot ini. Sengaja tanpa library pihak ketiga.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiBase = "https://api.telegram.org"

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// CallbackQuery dikirim saat pengguna menekan tombol inline.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// allowedUpdates harus sama untuk polling dan webhook; tanpa
// "callback_query", tekanan tombol tidak pernah sampai ke bot.
var allowedUpdates = []string{"message", "callback_query"}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	// Timeout harus lebih lama dari long polling getUpdates (30 detik).
	return &Client{token: token, http: &http.Client{Timeout: 40 * time.Second}}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error memuat URL lengkap, termasuk token. Jangan sampai masuk log.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()

	var r apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: respons tidak terbaca: %w", method, err)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %s", method, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

// SendMessage mengirim teks berformat HTML Telegram (<b>, <i>, <code>).
// kb boleh nil kalau pesan tidak butuh tombol.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, kb *InlineKeyboardMarkup) error {
	params := map[string]any{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if kb != nil {
		params["reply_markup"] = kb
	}
	return c.call(ctx, "sendMessage", params, nil)
}

// EditMessage mengganti teks dan tombol pesan yang sudah terkirim.
func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text string, kb *InlineKeyboardMarkup) error {
	params := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if kb != nil {
		params["reply_markup"] = kb
	}
	err := c.call(ctx, "editMessageText", params, nil)
	// Menekan tombol yang sama dua kali menghasilkan isi yang sama persis.
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

// AnswerCallback menghentikan ikon loading di tombol. text muncul sebagai
// notifikasi singkat; boleh kosong.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	}, nil)
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": allowedUpdates,
	}, &updates)
	return updates, err
}

// SetWebhook mendaftarkan URL yang akan dipanggil Telegram tiap ada pesan.
// Telegram akan mengirim secret di header X-Telegram-Bot-Api-Secret-Token.
func (c *Client) SetWebhook(ctx context.Context, webhookURL, secret string) error {
	return c.call(ctx, "setWebhook", map[string]any{
		"url":             webhookURL,
		"secret_token":    secret,
		"allowed_updates": allowedUpdates,
	}, nil)
}

func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}
