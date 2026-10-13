package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ayndri/restok/restock"
	"github.com/ayndri/restok/telegram"
)

// fakeStore meniru store.Store di memori, cukup untuk satu chat.
type fakeStore struct {
	items   []restock.Item // hanya ID, Name, GuessDays yang dipakai
	units   []fakeUnit
	actions []fakeAction
	seen    map[int64]bool
	nextID  int64
	pending *fakePending
}

type fakePending struct {
	unitID int64
	since  time.Time
}

type fakeUnit struct {
	id, itemID       int64
	price            int64
	bought, opened   time.Time
	finished         time.Time
	reminded, snooze time.Time
	expires          time.Time
	expReminded      time.Time
}

type fakeAction struct {
	kind           restock.Action
	unitID, opened int64
}

func (f *fakeStore) id() int64 { f.nextID++; return f.nextID }

func (f *fakeStore) once(updateID int64) error {
	if f.seen == nil {
		f.seen = map[int64]bool{}
	}
	if f.seen[updateID] {
		return restock.ErrDuplicate
	}
	f.seen[updateID] = true
	return nil
}

func (f *fakeStore) active(itemID int64) int {
	return slices.IndexFunc(f.units, func(u fakeUnit) bool {
		return u.itemID == itemID && !u.opened.IsZero() && u.finished.IsZero()
	})
}

func (f *fakeStore) Buy(_ context.Context, _, updateID int64, name string, price int64, at, expires time.Time) (restock.BuyResult, error) {
	if err := f.once(updateID); err != nil {
		return restock.BuyResult{}, err
	}
	var res restock.BuyResult
	i := slices.IndexFunc(f.items, func(it restock.Item) bool { return it.Name == name })
	if i < 0 {
		f.items = append(f.items, restock.Item{ID: f.id(), Name: name, RemindBefore: 5})
		i, res.NewItem = len(f.items)-1, true
	}
	res.ItemID = f.items[i].ID
	u := fakeUnit{id: f.id(), itemID: res.ItemID, price: price, bought: at, expires: expires}
	res.UnitID = u.id
	if f.active(res.ItemID) >= 0 {
		res.Spare = true
	} else {
		u.opened = at
	}
	f.units = append(f.units, u)
	f.actions = append(f.actions, fakeAction{kind: restock.Buy, unitID: u.id})
	return res, nil
}

func (f *fakeStore) Finish(_ context.Context, _, updateID, itemID int64, at time.Time) (restock.FinishResult, error) {
	if err := f.once(updateID); err != nil {
		return restock.FinishResult{}, err
	}
	a := f.active(itemID)
	if a < 0 {
		return restock.FinishResult{NothingActive: true}, nil
	}
	f.units[a].finished = at
	res := restock.FinishResult{Days: restock.CycleDays(f.units[a].opened, at)}
	act := fakeAction{kind: restock.Finish, unitID: f.units[a].id}
	if spares := f.spares(itemID); len(spares) > 0 {
		i := f.unit(spares[0].id)
		f.units[i].opened = at
		res.OpenedSpare, act.opened = true, f.units[i].id
	}
	f.actions = append(f.actions, act)
	return res, nil
}

// spares mengembalikan cadangan sesuai urutan dibuka: yang paling cepat
// kadaluwarsa dulu, yang tanpa tanggal paling akhir.
func (f *fakeStore) spares(itemID int64) []fakeUnit {
	var out []fakeUnit
	for _, u := range f.units {
		if u.itemID == itemID && u.opened.IsZero() {
			out = append(out, u)
		}
	}
	slices.SortStableFunc(out, func(a, b fakeUnit) int {
		switch {
		case a.expires.IsZero() == b.expires.IsZero():
			return a.expires.Compare(b.expires)
		case a.expires.IsZero():
			return 1
		}
		return -1
	})
	return out
}

func (f *fakeStore) unit(id int64) int {
	return slices.IndexFunc(f.units, func(u fakeUnit) bool { return u.id == id })
}

func (f *fakeStore) Undo(_ context.Context, _ int64) (restock.Undone, bool, error) {
	if len(f.actions) == 0 {
		return restock.Undone{}, false, nil
	}
	a := f.actions[len(f.actions)-1]
	f.actions = f.actions[:len(f.actions)-1]
	u := f.units[f.unit(a.unitID)]
	it, _, _ := f.Item(context.Background(), 0, u.itemID)
	if a.kind == restock.Buy {
		f.units = slices.Delete(f.units, f.unit(a.unitID), f.unit(a.unitID)+1)
	} else {
		f.units[f.unit(a.unitID)].finished = time.Time{}
		if a.opened != 0 {
			f.units[f.unit(a.opened)].opened = time.Time{}
		}
	}
	return restock.Undone{Action: a.kind, Name: it.Name}, true, nil
}

func (f *fakeStore) Items(_ context.Context, _ int64) ([]restock.Item, error) {
	var out []restock.Item
	for _, base := range f.items {
		it := base
		for _, u := range f.units {
			if u.itemID != it.ID {
				continue
			}
			unit := restock.Unit{ID: u.id, ItemID: u.itemID, Price: u.price, BoughtAt: u.bought,
				OpenedAt: u.opened, RemindedAt: u.reminded, SnoozeUntil: u.snooze,
				ExpiresOn: u.expires, ExpiryRemindedAt: u.expReminded}
			switch {
			case u.opened.IsZero():
				it.Spares++
			case !u.finished.IsZero():
				it.Cycles = append(it.Cycles, restock.CycleDays(u.opened, u.finished))
			default:
				it.Active = &unit
			}
		}
		for _, u := range f.spares(it.ID) {
			it.SpareUnits = append(it.SpareUnits, restock.Unit{ID: u.id, ItemID: u.itemID, BoughtAt: u.bought,
				ExpiresOn: u.expires, ExpiryRemindedAt: u.expReminded})
		}
		out = append(out, it)
	}
	return out, nil
}

func (f *fakeStore) Item(ctx context.Context, chatID, itemID int64) (restock.Item, bool, error) {
	items, _ := f.Items(ctx, chatID)
	for _, it := range items {
		if it.ID == itemID {
			return it, true, nil
		}
	}
	return restock.Item{}, false, nil
}

func (f *fakeStore) ItemByUnit(ctx context.Context, chatID, unitID int64) (restock.Item, bool, error) {
	i := f.unit(unitID)
	if i < 0 {
		return restock.Item{}, false, nil
	}
	return f.Item(ctx, chatID, f.units[i].itemID)
}

func (f *fakeStore) SetGuess(_ context.Context, _, itemID int64, days int) (bool, error) {
	for i := range f.items {
		if f.items[i].ID == itemID {
			f.items[i].GuessDays = days
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) MarkReminded(_ context.Context, unitID int64, at time.Time) error {
	f.units[f.unit(unitID)].reminded = at
	return nil
}

func (f *fakeStore) Snooze(_ context.Context, unitID int64, until time.Time) error {
	i := f.unit(unitID)
	f.units[i].snooze, f.units[i].reminded = until, time.Time{}
	return nil
}

func (f *fakeStore) SetExpiry(_ context.Context, _, unitID int64, day time.Time) (bool, error) {
	i := f.unit(unitID)
	if i < 0 {
		return false, nil
	}
	f.units[i].expires, f.units[i].expReminded = day, time.Time{}
	return true, nil
}

func (f *fakeStore) MarkExpiryReminded(_ context.Context, unitID int64, at time.Time) error {
	f.units[f.unit(unitID)].expReminded = at
	return nil
}

func (f *fakeStore) SetPending(_ context.Context, _, unitID int64, at time.Time) error {
	f.pending = &fakePending{unitID, at}
	return nil
}

func (f *fakeStore) TakePending(context.Context, int64) (int64, time.Time, bool, error) {
	p := f.pending
	f.pending = nil
	if p == nil || f.unit(p.unitID) < 0 {
		return 0, time.Time{}, false, nil
	}
	return p.unitID, p.since, true, nil
}

func (f *fakeStore) Chats(context.Context) ([]int64, error) {
	if len(f.items) == 0 {
		return nil, nil
	}
	return []int64{chat}, nil
}

type sent struct {
	text string
	kb   *telegram.InlineKeyboardMarkup
}

type fakeSender struct {
	msgs    []sent
	edits   []sent
	notices []string
}

func (s *fakeSender) SendMessage(_ context.Context, _ int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	s.msgs = append(s.msgs, sent{text, kb})
	return nil
}

func (s *fakeSender) EditMessage(_ context.Context, _, _ int64, text string, kb *telegram.InlineKeyboardMarkup) error {
	s.edits = append(s.edits, sent{text, kb})
	return nil
}

func (s *fakeSender) AnswerCallback(_ context.Context, _, text string) error {
	s.notices = append(s.notices, text)
	return nil
}

const chat = 42

type harness struct {
	t      *testing.T
	bot    *Bot
	store  *fakeStore
	sender *fakeSender
	now    time.Time
	update int64
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, store: &fakeStore{}, sender: &fakeSender{},
		now: time.Date(2026, 9, 1, 9, 0, 0, 0, restock.WIB)}
	h.bot = &Bot{Store: h.store, Sender: h.sender, Now: func() time.Time { return h.now }}
	return h
}

// say mengirim pesan dan mengembalikan balasan terakhir.
func (h *harness) say(msg string) sent {
	h.t.Helper()
	h.update++
	err := h.bot.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: h.update,
		Message:  &telegram.Message{Chat: telegram.Chat{ID: chat}, Text: msg},
	})
	if err != nil {
		h.t.Fatalf("%q: %v", msg, err)
	}
	return h.sender.msgs[len(h.sender.msgs)-1]
}

// press menekan tombol dan mengembalikan isi pesan setelah diedit.
func (h *harness) press(data string) string {
	h.t.Helper()
	h.update++
	edits := len(h.sender.edits)
	err := h.bot.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: h.update,
		CallbackQuery: &telegram.CallbackQuery{ID: "q", Data: data,
			Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: chat}}},
	})
	if err != nil {
		h.t.Fatalf("tombol %q: %v", data, err)
	}
	if len(h.sender.edits) == edits {
		h.t.Fatalf("tombol %q tidak mengedit pesan", data)
	}
	return h.sender.edits[len(h.sender.edits)-1].text
}

func (h *harness) days(n int) { h.now = h.now.AddDate(0, 0, n) }

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("pesan tidak memuat %q:\n%s", w, got)
		}
	}
}

func buttons(kb *telegram.InlineKeyboardMarkup) []string {
	var out []string
	if kb == nil {
		return nil
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			out = append(out, b.CallbackData)
		}
	}
	return out
}

func TestFirstPurchaseAsksGuess(t *testing.T) {
	h := newHarness(t)
	r := h.say("beli sunscreen 89rb")
	mustContain(t, r.text, "<b>Sunscreen</b> dicatat", "Rp89.000", "berapa lama")
	if !slices.Contains(buttons(r.kb), "g:1:30") {
		t.Fatalf("tombol tebakan tidak ada: %v", buttons(r.kb))
	}

	// 1 Sep + 30 hari = 1 Okt.
	mustContain(t, h.press("g:1:30"), "±30 hari", "1 Okt")
}

func TestCycleIsLearned(t *testing.T) {
	h := newHarness(t)
	h.say("beli sunscreen")
	h.press("g:1:30")

	h.days(40)
	mustContain(t, h.say("habis sunscreen").text, "habis setelah <b>40 hari</b>", "Nggak ada cadangan")

	h.say("beli sunscreen")
	h.days(44)
	r := h.say("habis Sunscreen")
	mustContain(t, r.text, "44 hari", "±42 hari")

	// Pembelian berikutnya memakai perkiraan dari riwayat, bukan tebakan 30 hari.
	mustContain(t, h.say("beli sunscreen").text, "±42 hari")
}

func TestSpareIsOpenedWhenFinished(t *testing.T) {
	h := newHarness(t)
	h.say("beli sabun cuci muka 45rb")
	mustContain(t, h.say("beli sabun cuci muka 45rb").text, "cadangan", "Total cadangan: 1")

	h.days(30)
	mustContain(t, h.say("habis sabun cuci muka").text, "Cadangan mulai dipakai")

	stock := h.say("/stok").text
	mustContain(t, stock, "Sabun cuci muka")
	if strings.Contains(stock, "📦") {
		t.Errorf("cadangan seharusnya sudah terpakai:\n%s", stock)
	}
}

func TestUndoFinishRestoresSpare(t *testing.T) {
	h := newHarness(t)
	h.say("beli serum")
	h.say("beli serum")
	h.days(10)
	h.say("habis serum")

	mustContain(t, h.say("/batal").text, "Dibatalkan", "<b>Serum</b> habis")
	it, _, _ := h.store.Item(context.Background(), chat, 1)
	if it.Spares != 1 || it.Active == nil || len(it.Cycles) != 0 {
		t.Errorf("setelah /batal: cadangan %d, aktif %v, siklus %v", it.Spares, it.Active != nil, it.Cycles)
	}
}

func TestDuplicateUpdateIgnored(t *testing.T) {
	h := newHarness(t)
	h.say("beli serum")
	msgs := len(h.sender.msgs)
	err := h.bot.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: h.update, // sama dengan kiriman terakhir
		Message:  &telegram.Message{Chat: telegram.Chat{ID: chat}, Text: "beli serum"},
	})
	if err != nil || len(h.sender.msgs) != msgs {
		t.Fatalf("kiriman ulang dibalas lagi (err %v)", err)
	}
	if len(h.store.units) != 1 {
		t.Errorf("unit tercatat %d kali, mau 1", len(h.store.units))
	}
}

func TestFinishAmbiguousAsks(t *testing.T) {
	h := newHarness(t)
	h.say("beli sabun cuci muka")
	h.say("beli sabun mandi")
	r := h.say("habis sabun")
	mustContain(t, r.text, "yang mana")
	if got := buttons(r.kb); !slices.Equal(got, []string{"f:1", "f:3"}) {
		t.Fatalf("tombol pilihan = %v", got)
	}
	mustContain(t, h.press("f:3"), "<b>Sabun mandi</b> habis")
}

func TestBackdated(t *testing.T) {
	h := newHarness(t) // Selasa 1 Sep 09.00
	r := h.say("beli sunscreen 89rb 25/8")
	mustContain(t, r.text, "<b>Sunscreen</b> dicatat", "Rp89.000", "tanggal 25 Agu")
	if got := h.store.units[0].opened; !got.Equal(time.Date(2026, 8, 25, 9, 0, 0, 0, restock.WIB)) {
		t.Fatalf("mulai dipakai %v, mau 25 Agu 09.00", got)
	}

	// Lupa lapor habis juga bisa: 25 Agu → 31 Agu = 6 hari.
	mustContain(t, h.say("kemarin habis sunscreen").text, "habis setelah <b>6 hari</b>", "tanggal 31 Agu")

	// Catatan hari ini tidak diberi baris tanggal.
	if r := h.say("beli sunscreen"); strings.Contains(r.text, "📅") {
		t.Errorf("catatan hari ini diberi tanggal:\n%s", r.text)
	}
}

func TestBackdatedRejected(t *testing.T) {
	h := newHarness(t)
	mustContain(t, h.say("beli serum 5/9").text, "belum lewat")
	if len(h.store.units) != 0 {
		t.Fatal("tanggal masa depan tetap tercatat")
	}

	h.say("beli serum")
	mustContain(t, h.say("kemarin habis serum").text, "baru mulai dipakai tanggal 1 Sep")
	if it, _, _ := h.store.Item(context.Background(), chat, 1); it.Active == nil {
		t.Error("serum tercatat habis padahal tanggalnya sebelum dibuka")
	}
}

func TestFinishAmbiguousKeepsDate(t *testing.T) {
	h := newHarness(t)
	h.say("beli sabun cuci muka")
	h.say("kemarin lusa beli sabun mandi") // 30 Agu
	h.days(5)                              // Minggu 6 Sep
	r := h.say("habis sabun kemarin")
	if got := buttons(r.kb); !slices.Equal(got, []string{"f:1:20260905", "f:3:20260905"}) {
		t.Fatalf("tombol pilihan = %v", got)
	}
	mustContain(t, h.press("f:3:20260905"), "<b>Sabun mandi</b> habis setelah <b>6 hari</b>", "tanggal 5 Sep")
}

// tap menekan tombol yang membalas dengan pesan baru, bukan mengedit.
func (h *harness) tap(data string) string {
	h.t.Helper()
	h.update++
	msgs := len(h.sender.msgs)
	err := h.bot.HandleUpdate(context.Background(), telegram.Update{
		UpdateID: h.update,
		CallbackQuery: &telegram.CallbackQuery{ID: "q", Data: data,
			Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: chat}}},
	})
	if err != nil || len(h.sender.msgs) == msgs {
		h.t.Fatalf("tombol %q tidak mengirim pesan (err %v)", data, err)
	}
	return h.sender.msgs[len(h.sender.msgs)-1].text
}

func TestExpiryInline(t *testing.T) {
	h := newHarness(t)
	r := h.say("beli sunscreen 89rb exp 8/2027")
	mustContain(t, r.text, "<b>Sunscreen</b> dicatat", "Rp89.000", "Kadaluwarsa 31 Agu 2027")
	if slices.Contains(buttons(r.kb), "e:2") {
		t.Error("tombol kadaluwarsa muncul padahal tanggalnya sudah diisi")
	}
	want := time.Date(2027, 8, 31, 0, 0, 0, 0, restock.WIB)
	if got := h.store.units[0].expires; !got.Equal(want) {
		t.Errorf("kadaluwarsa %v, mau %v", got, want)
	}

	mustContain(t, h.say("kemarin beli toner ed 15/8/27").text, "Kadaluwarsa 15 Agu 2027", "tanggal 31 Agu")
	mustContain(t, h.say("beli serum exp 1/2026").text, "udah lewat")
	mustContain(t, h.say("beli serum exp besok").text, "nggak kebaca")
	mustContain(t, h.say("habis sunscreen exp 8/2027").text, "dicatat waktu beli")
}

func TestExpiryButton(t *testing.T) {
	h := newHarness(t)
	r := h.say("beli serum")
	if got := buttons(r.kb); !slices.Contains(got, "g:1:30") || !slices.Contains(got, "e:2") {
		t.Fatalf("tombol pembelian pertama = %v", got)
	}
	// Setelah tebakan dijawab, tombol kadaluwarsa tetap ada.
	h.press("g:1:30")
	if got := buttons(h.sender.edits[len(h.sender.edits)-1].kb); !slices.Equal(got, []string{"e:2"}) {
		t.Fatalf("tombol setelah tebakan = %v", got)
	}

	mustContain(t, h.tap("e:2"), "Kapan kadaluwarsanya <b>Serum</b>")
	mustContain(t, h.say("1/2026").text, "udah lewat") // salah tanggal: tetap ditunggu
	r = h.say("15/9")
	mustContain(t, r.text, "Kadaluwarsa <b>Serum</b>: <b>15 Sep 2026</b>", "lebih cepat dari perkiraan habisnya (1 Okt)")

	// Pembelian berikutnya menawarkan tombol saja, tanpa tebakan.
	r = h.say("beli serum")
	if got := buttons(r.kb); !slices.Equal(got, []string{"e:3"}) {
		t.Fatalf("tombol pembelian kedua = %v", got)
	}
}

func TestExpiryPendingCancelled(t *testing.T) {
	h := newHarness(t)
	h.say("beli serum")
	h.tap("e:2")
	// Pesan biasa membatalkan penantian dan diproses seperti biasa.
	mustContain(t, h.say("beli sabun").text, "<b>Sabun</b> dicatat")
	mustContain(t, h.say("8/2027").text, "buat barang apa")
	if !h.store.units[0].expires.IsZero() {
		t.Error("tanggal tetap tersimpan padahal penantian sudah batal")
	}

	// Penantian kedaluwarsa setelah sehari.
	h.tap("e:2")
	h.days(2)
	mustContain(t, h.say("8/2027").text, "buat barang apa")
}

func TestSpareWithEarliestExpiryOpensFirst(t *testing.T) {
	h := newHarness(t)
	h.say("beli sabun")
	h.say("beli sabun exp 12/2026")
	h.say("beli sabun exp 10/2026")
	h.say("beli sabun") // tanpa tanggal, paling akhir
	h.days(20)
	h.say("habis sabun")
	it, _, _ := h.store.Item(context.Background(), chat, 1)
	want := time.Date(2026, 10, 31, 0, 0, 0, 0, restock.WIB)
	if it.Active == nil || !it.Active.ExpiresOn.Equal(want) {
		t.Fatalf("cadangan yang dibuka: %+v, mau yang kadaluwarsa 31 Okt", it.Active)
	}
	mustContain(t, h.say("/stok").text, "exp 31 Okt 2026", "2 cadangan")
}

func TestDailyExpiryReminder(t *testing.T) {
	h := newHarness(t)                // 1 Sep
	h.say("beli serum exp 20/9/2026") // diingatkan mulai 6 Sep
	h.say("beli serum exp 10/9/2026") // cadangan, langsung masuk 14 hari
	if n, err := h.bot.RunDaily(context.Background()); n != 1 || err != nil {
		t.Fatalf("1 Sep: terkirim %d (err %v), mau 1", n, err)
	}
	mustContain(t, h.sender.msgs[len(h.sender.msgs)-1].text, "yang cadangan", "9 hari lagi", "10 Sep 2026")

	h.now = time.Date(2026, 9, 5, 8, 0, 0, 0, restock.WIB)
	if n, _ := h.bot.RunDaily(context.Background()); n != 0 {
		t.Fatalf("5 Sep: terkirim %d, mau 0", n)
	}
	h.days(1)
	if n, _ := h.bot.RunDaily(context.Background()); n != 1 {
		t.Fatalf("6 Sep: terkirim %d, mau 1", n)
	}
	mustContain(t, h.sender.msgs[len(h.sender.msgs)-1].text, "<b>Serum</b> kadaluwarsa <b>14 hari lagi</b>", "habis serum")
	h.days(1)
	if n, _ := h.bot.RunDaily(context.Background()); n != 0 {
		t.Errorf("pengingat kadaluwarsa dikirim ulang %d kali", n)
	}
}

func TestFinishUnknownItem(t *testing.T) {
	h := newHarness(t)
	mustContain(t, h.say("habis serum").text, "Belum ada <b>Serum</b>", "beli serum")
}

func TestDailyReminder(t *testing.T) {
	h := newHarness(t)
	h.say("beli sunscreen")
	h.press("g:1:40") // perkiraan habis 11 Okt, pengingat mulai 6 Okt

	h.now = time.Date(2026, 10, 5, 8, 0, 0, 0, restock.WIB)
	if n, err := h.bot.RunDaily(context.Background()); n != 0 || err != nil {
		t.Fatalf("5 Okt: terkirim %d (err %v), mau 0", n, err)
	}

	h.days(1)
	msgs := len(h.sender.msgs)
	if n, err := h.bot.RunDaily(context.Background()); n != 1 || err != nil {
		t.Fatalf("6 Okt: terkirim %d (err %v), mau 1", n, err)
	}
	r := h.sender.msgs[msgs]
	mustContain(t, r.text, "<b>Sunscreen</b>", "5 hari lagi", "11 Okt")
	if got := buttons(r.kb); !slices.Equal(got, []string{"rb:2", "rm:2", "rh:2"}) {
		t.Fatalf("tombol pengingat = %v", got)
	}

	// Besoknya tidak dikirim lagi.
	h.days(1)
	if n, _ := h.bot.RunDaily(context.Background()); n != 0 {
		t.Errorf("pengingat dikirim ulang %d kali", n)
	}

	// "Masih ada" menunda 3 hari.
	mustContain(t, h.press("rm:2"), "3 hari lagi")
	h.days(2)
	if n, _ := h.bot.RunDaily(context.Background()); n != 0 {
		t.Errorf("masih dalam masa tunda, tapi terkirim %d", n)
	}
	h.days(1)
	if n, _ := h.bot.RunDaily(context.Background()); n != 1 {
		t.Errorf("masa tunda selesai, terkirim %d, mau 1", n)
	}
}

func TestReminderBoughtThenStale(t *testing.T) {
	h := newHarness(t)
	h.say("beli sunscreen")
	h.press("g:1:30")
	h.days(28)
	h.bot.RunDaily(context.Background())

	mustContain(t, h.press("rb:2"), "Cadangan <b>Sunscreen</b> dicatat")
	// Ditekan lagi: sudah ada cadangan, tidak menambah unit kedua.
	mustContain(t, h.press("rb:2"), "udah punya cadangan")
	if it, _, _ := h.store.Item(context.Background(), chat, 1); it.Spares != 1 {
		t.Errorf("cadangan %d, mau 1", it.Spares)
	}

	mustContain(t, h.press("rh:2"), "habis setelah <b>28 hari</b>", "Cadangan mulai dipakai")
	// Unit 2 sudah habis, jadi tombol dari pengingat lama tidak berlaku lagi.
	mustContain(t, h.press("rh:2"), "nggak berlaku")
}

func TestStockOrder(t *testing.T) {
	h := newHarness(t)
	h.say("beli serum")     // tanpa perkiraan
	h.say("beli sunscreen") // perkiraan 30 hari
	h.press("g:3:30")
	h.say("beli shampo")
	h.press("g:5:14")
	h.say("beli sikat gigi")
	h.days(3)
	h.say("habis sikat gigi") // habis, belum beli lagi

	got := h.say("/stok").text
	order := []string{"Shampo", "Sunscreen", "Serum", "Sikat gigi"}
	last := -1
	for _, name := range order {
		i := strings.Index(got, "<b>"+name+"</b>")
		if i <= last {
			t.Fatalf("urutan salah, mau %v:\n%s", order, got)
		}
		last = i
	}
	mustContain(t, got, "habis, belum beli lagi", "belum ada perkiraan", "11 hari lagi")
}

func TestHints(t *testing.T) {
	h := newHarness(t)
	for msg, want := range map[string]string{
		"halo":         "dua hal",
		"beli 20rb":    "Nama barangnya apa",
		"/stok":        "Belum ada barang",
		"/batal":       "Belum ada catatan",
		"/entahlah":    "belum ada",
		"/Stok@restok": "Belum ada barang",
	} {
		mustContain(t, h.say(msg).text, want)
	}
}

func TestPrivateBot(t *testing.T) {
	h := newHarness(t)
	h.bot.Allowed = map[int64]bool{7: true}
	mustContain(t, h.say("beli serum").text, fmt.Sprintf("<code>%d</code>", chat))
	if len(h.store.items) != 0 {
		t.Error("chat yang tidak diizinkan tetap tercatat")
	}
}
