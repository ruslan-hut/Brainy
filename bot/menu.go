package bot

import (
	"Brainy/lib/sl"
	"context"
	"log/slog"
	"strings"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const menuCallbackPrefix = "menu"

// menuKeyboard is the top-level user menu shown by /menu.
func menuKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Set topic", CallbackData: menuCallbackPrefix + ":topic"}},
		{{Text: "Clear context", CallbackData: menuCallbackPrefix + ":clear"}},
	}}
}

// sendMenu posts the /menu inline keyboard.
func (t *TgBot) sendMenu(ctx context.Context, chatID int64) {
	if _, err := t.api.SendMessage(ctx, &tg.SendMessageParams{
		ChatID:      chatID,
		Text:        "Menu:",
		ReplyMarkup: menuKeyboard(),
	}); err != nil {
		t.log.With(slog.Int64("id", chatID)).Warn("sending menu", sl.Err(err))
	}
}

// handleMenuCallback dispatches user menu button taps.
func (t *TgBot) handleMenuCallback(ctx context.Context, cb *models.CallbackQuery) {
	defer t.answerCallback(ctx, cb.ID)

	parts := strings.SplitN(cb.Data, ":", 2)
	msg := cb.Message.Message
	if len(parts) != 2 || msg == nil {
		return
	}
	chatID := msg.Chat.ID
	msgID := msg.ID
	userID := cb.From.ID

	switch parts[1] {
	case "topic":
		t.awaitingTopic.Store(userID, struct{}{})
		t.editPlain(ctx, chatID, msgID, "Send me the new topic.")
	case "clear":
		t.chat.ClearContext(ctx, chatID)
		t.log.With(slog.Int64("user", userID)).Info("context cleared via menu")
		t.editPlain(ctx, chatID, msgID, "Context cleared.")
	case "topic_change":
		t.awaitingTopic.Store(userID, struct{}{})
		t.editPlain(ctx, chatID, msgID, "Send me the new topic.")
	case "topic_clear":
		t.chat.SetTopic(ctx, chatID, "")
		t.editPlain(ctx, chatID, msgID, "Topic cleared.")
	case "topic_exit":
		current := t.chat.GetTopic(ctx, chatID)
		if current == "" {
			t.editPlain(ctx, chatID, msgID, "No topic set.")
			return
		}
		t.editPlain(ctx, chatID, msgID, "Topic kept: "+current)
	}
}

// sendTopicMenu shows the current topic with Change/Clear/Exit buttons.
func (t *TgBot) sendTopicMenu(ctx context.Context, chatID int64, current string) {
	if _, err := t.api.SendMessage(ctx, &tg.SendMessageParams{
		ChatID: chatID,
		Text:   "Current topic: " + current,
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "Change", CallbackData: menuCallbackPrefix + ":topic_change"},
			{Text: "Clear", CallbackData: menuCallbackPrefix + ":topic_clear"},
			{Text: "Exit", CallbackData: menuCallbackPrefix + ":topic_exit"},
		}}},
	}); err != nil {
		t.log.With(slog.Int64("id", chatID)).Warn("sending topic menu", sl.Err(err))
	}
}

func (t *TgBot) editPlain(ctx context.Context, chatID int64, msgID int, text string) {
	if _, err := t.api.EditMessageText(ctx, &tg.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: text}); err != nil {
		t.log.With(slog.Int64("id", chatID)).Debug("menu edit", sl.Err(err))
	}
}
