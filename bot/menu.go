package bot

import (
	"Brainy/lib/sl"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const menuCallbackPrefix = "menu"

// menuKeyboard is the top-level user menu shown by /menu.
func menuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Set topic", menuCallbackPrefix+":topic"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Clear context", menuCallbackPrefix+":clear"),
		),
	)
}

// sendMenu posts the /menu inline keyboard.
func (t *TgBot) sendMenu(chatID int64) {
	msg := tgbotapi.NewMessage(chatID, "Menu:")
	msg.ReplyMarkup = menuKeyboard()
	if _, err := t.api.Send(msg); err != nil {
		t.log.With(slog.Int64("id", chatID)).Warn("sending menu", sl.Err(err))
	}
}

// handleMenuCallback dispatches user menu button taps.
func (t *TgBot) handleMenuCallback(cb *tgbotapi.CallbackQuery) {
	defer func() {
		if _, err := t.api.Request(tgbotapi.NewCallback(cb.ID, "")); err != nil {
			t.log.Debug("answering menu callback", sl.Err(err))
		}
	}()

	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	userID := cb.From.ID

	switch parts[1] {
	case "topic":
		t.awaitingTopic.Store(userID, struct{}{})
		t.editPlain(chatID, msgID, "Send me the new topic.")
	case "clear":
		t.chat.ClearContext(chatID)
		t.log.With(slog.Int64("user", userID)).Info("context cleared via menu")
		t.editPlain(chatID, msgID, "Context cleared.")
	}
}

func (t *TgBot) editPlain(chatID int64, msgID int, text string) {
	if _, err := t.api.Send(tgbotapi.NewEditMessageText(chatID, msgID, text)); err != nil {
		t.log.With(slog.Int64("id", chatID)).Debug("menu edit", sl.Err(err))
	}
}
