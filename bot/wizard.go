package bot

import (
	"Brainy/storage"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"Brainy/lib/sl"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// callbackPrefix identifies callback queries that belong to the tuneup wizard.
// Format: tune:<step>:<value> — value "" means skip.
const callbackPrefix = "tune"

// wizardOption is one button on a wizard step keyboard.
type wizardOption struct {
	label string
	value string
}

// wizardStep declares a single question in the tuneup flow.
type wizardStep struct {
	prompt  string
	options []wizardOption
	apply   func(*storage.UserPreferences, string)
}

// wizardSteps is the static script the wizard walks through.
var wizardSteps = []wizardStep{
	{
		prompt: "Step 1/5 — Preferred language?",
		options: []wizardOption{
			{"English", "English"},
			{"Українська", "Ukrainian"},
			{"Español", "Spanish"},
			{"Català", "Catalan"},
			{"Русский", "Russian"},
			{"Skip", ""},
		},
		apply: func(p *storage.UserPreferences, v string) { p.PreferredLanguage = v },
	},
	{
		prompt: "Step 2/5 — Tone?",
		options: []wizardOption{
			{"Formal", "formal"},
			{"Informal", "informal"},
			{"Neutral", "neutral"},
			{"Skip", ""},
		},
		apply: func(p *storage.UserPreferences, v string) { p.Formality = v },
	},
	{
		prompt: "Step 3/5 — Response length?",
		options: []wizardOption{
			{"Short", "short"},
			{"Medium", "medium"},
			{"Long", "long"},
			{"Skip", ""},
		},
		apply: func(p *storage.UserPreferences, v string) { p.ResponseLength = v },
	},
	{
		prompt: "Step 4/5 — Humor?",
		options: []wizardOption{
			{"None", "none"},
			{"Occasional", "occasional"},
			{"Frequent", "frequent"},
			{"Skip", ""},
		},
		apply: func(p *storage.UserPreferences, v string) { p.HumorPreference = v },
	},
	{
		prompt: "Step 5/5 — Technical level?",
		options: []wizardOption{
			{"Beginner", "beginner"},
			{"Intermediate", "intermediate"},
			{"Expert", "expert"},
			{"Skip", ""},
		},
		apply: func(p *storage.UserPreferences, v string) { p.TechnicalLevel = v },
	},
}

// wizardSession is a user's in-flight tuneup. Keyed by userID in the manager.
type wizardSession struct {
	ownerID   int64 // userID who started the wizard; only this user may tap buttons
	chatID    int64
	messageID int
	step      int
	prefs     *storage.UserPreferences
}

// wizardManager owns active sessions and the prefs storage they persist to.
type wizardManager struct {
	mu       sync.Mutex
	sessions map[int64]*wizardSession
	prefs    storage.PreferencesStorage
	api      *tgbotapi.BotAPI
	log      *slog.Logger
}

func newWizardManager(api *tgbotapi.BotAPI, prefs storage.PreferencesStorage, log *slog.Logger) *wizardManager {
	return &wizardManager{
		sessions: make(map[int64]*wizardSession),
		prefs:    prefs,
		api:      api,
		log:      log.With(sl.Module("wizard")),
	}
}

// start begins or restarts a tuneup session for userID in chatID.
func (w *wizardManager) start(chatID, userID int64) {
	if w.prefs == nil {
		w.send(chatID, "Preferences storage is not configured.")
		return
	}
	existing, _ := w.prefs.GetUserPreferences(userID)
	prefs := &storage.UserPreferences{UserId: userID, CreatedAt: time.Now()}
	if existing != nil {
		*prefs = *existing
	}
	prefs.ManuallySet = true

	msg := tgbotapi.NewMessage(chatID, wizardSteps[0].prompt)
	msg.ReplyMarkup = buildKeyboard(0, wizardSteps[0].options)
	sent, err := w.api.Send(msg)
	if err != nil {
		w.log.With(slog.Int64("user", userID)).Error("starting wizard", sl.Err(err))
		return
	}

	w.mu.Lock()
	w.sessions[userID] = &wizardSession{
		ownerID:   userID,
		chatID:    chatID,
		messageID: sent.MessageID,
		step:      0,
		prefs:     prefs,
	}
	w.mu.Unlock()
}

// handleCallback is called for every CallbackQuery whose data starts with callbackPrefix.
func (w *wizardManager) handleCallback(cb *tgbotapi.CallbackQuery) {
	// Always ack so the spinner clears.
	defer func() {
		if _, err := w.api.Request(tgbotapi.NewCallback(cb.ID, "")); err != nil {
			w.log.Debug("answering callback", sl.Err(err))
		}
	}()

	step, value, ok := parseCallback(cb.Data)
	if !ok {
		return
	}

	userID := cb.From.ID
	w.mu.Lock()
	sess, found := w.sessions[userID]
	w.mu.Unlock()
	if !found {
		// Either a stale button after restart, or a foreign user tapping
		// someone else's wizard. Either way: silent ack, leave message alone.
		return
	}
	if cb.Message.MessageID != sess.messageID {
		// Tap on a different (older) wizard message — ignore.
		return
	}
	if step != sess.step {
		return
	}

	if value != "" {
		wizardSteps[step].apply(sess.prefs, value)
	}
	sess.step++

	if sess.step >= len(wizardSteps) {
		w.finish(userID, sess)
		return
	}

	next := wizardSteps[sess.step]
	kb := buildKeyboard(sess.step, next.options)
	w.editText(sess.chatID, sess.messageID, next.prompt, &kb)
}

func (w *wizardManager) finish(userID int64, sess *wizardSession) {
	now := time.Now()
	sess.prefs.UpdatedAt = now
	if sess.prefs.CreatedAt.IsZero() {
		sess.prefs.CreatedAt = now
	}
	if err := w.prefs.SaveUserPreferences(sess.prefs); err != nil {
		w.log.With(slog.Int64("user", userID)).Error("saving prefs", sl.Err(err))
		w.editText(sess.chatID, sess.messageID, "Sorry, couldn't save your preferences. Try again later.", nil)
	} else {
		w.editText(sess.chatID, sess.messageID, "Done. Your preferences are saved:\n"+summarize(sess.prefs), nil)
	}
	w.mu.Lock()
	delete(w.sessions, userID)
	w.mu.Unlock()
}

func (w *wizardManager) send(chatID int64, text string) {
	if _, err := w.api.Send(tgbotapi.NewMessage(chatID, text)); err != nil {
		w.log.With(slog.Int64("id", chatID)).Warn("send", sl.Err(err))
	}
}

func (w *wizardManager) editText(chatID int64, messageID int, text string, kb *tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	if kb != nil {
		edit.ReplyMarkup = kb
	}
	if _, err := w.api.Send(edit); err != nil {
		w.log.With(slog.Int64("id", chatID)).Debug("wizard edit", sl.Err(err))
	}
}

func buildKeyboard(step int, opts []wizardOption) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(opts))
	for _, o := range opts {
		data := fmt.Sprintf("%s:%d:%s", callbackPrefix, step, o.value)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(o.label, data),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func parseCallback(data string) (step int, value string, ok bool) {
	parts := strings.SplitN(data, ":", 3)
	if len(parts) != 3 || parts[0] != callbackPrefix {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, "", false
	}
	return n, parts[2], true
}

func summarize(p *storage.UserPreferences) string {
	var b strings.Builder
	add := func(label, v string) {
		if v != "" {
			fmt.Fprintf(&b, "- %s: %s\n", label, v)
		}
	}
	add("Language", p.PreferredLanguage)
	add("Tone", p.Formality)
	add("Length", p.ResponseLength)
	add("Humor", p.HumorPreference)
	add("Technical level", p.TechnicalLevel)
	if b.Len() == 0 {
		return "(no preferences set — defaults apply)"
	}
	return b.String()
}
