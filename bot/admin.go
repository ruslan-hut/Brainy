package bot

import (
	"Brainy/lib/sl"
	"Brainy/storage"
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const adminCallbackPrefix = "admin"

const inviteListLimit = 50

// generateInviteCode returns a short, unambiguous code (uppercase base32, no padding).
func generateInviteCode() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), nil
}

// adminMenuKeyboard is the top-level inline menu shown by /admin.
func adminMenuKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: "Generate code", CallbackData: adminCallbackPrefix + ":gen"}},
		{{Text: "List codes", CallbackData: adminCallbackPrefix + ":list"}},
	}}
}

// isAdmin reports whether userId currently has admin role.
func (t *TgBot) isAdmin(ctx context.Context, userId int64) bool {
	if t.users == nil {
		return false
	}
	u, err := t.users.GetUser(ctx, userId)
	if err != nil || u == nil {
		return false
	}
	return u.Role == storage.RoleAdmin
}

// isAuthorized reports whether userId is registered (any role).
func (t *TgBot) isAuthorized(ctx context.Context, userId int64) bool {
	if t.users == nil {
		return true // gating disabled if users storage not wired
	}
	u, err := t.users.GetUser(ctx, userId)
	return err == nil && u != nil
}

// seedAdmins ensures every config-listed user is registered as admin at startup.
func (t *TgBot) seedAdmins(ctx context.Context, ids []int64) {
	if t.users == nil {
		return
	}
	for _, id := range ids {
		existing, _ := t.users.GetUser(ctx, id)
		if existing != nil && existing.Role == storage.RoleAdmin {
			continue
		}
		u := &storage.User{
			UserId:   id,
			Role:     storage.RoleAdmin,
			JoinedAt: time.Now(),
		}
		if existing != nil {
			u.Username = existing.Username
			u.InviteCode = existing.InviteCode
			u.JoinedAt = existing.JoinedAt
		}
		if err := t.users.SaveUser(ctx, u); err != nil {
			t.log.With(slog.Int64("user", id)).Error("seeding admin", sl.Err(err))
		} else {
			t.log.With(slog.Int64("user", id)).Info("seeded admin")
		}
	}
}

// redeemAndRegister redeems code for a new user and persists their record.
func (t *TgBot) redeemAndRegister(ctx context.Context, userId int64, username, code string) error {
	if t.invites == nil || t.users == nil {
		return fmt.Errorf("invites disabled")
	}
	ok, err := t.invites.RedeemInvite(ctx, code, userId)
	if err != nil {
		return fmt.Errorf("redeeming: %w", err)
	}
	if !ok {
		return fmt.Errorf("invalid or used code")
	}
	u := &storage.User{
		UserId:     userId,
		Username:   username,
		Role:       storage.RoleUser,
		InviteCode: code,
		JoinedAt:   time.Now(),
	}
	return t.users.SaveUser(ctx, u)
}

// handleStart processes /start. With an arg it redeems immediately; without
// one it prompts the user for their invite code (consumed by the next message).
// messageID is the /start message itself; deleted on successful redemption so
// the code doesn't linger in the chat history.
func (t *TgBot) handleStart(ctx context.Context, chatID, userID int64, messageID int, username, code string) {
	if t.isAuthorized(ctx, userID) {
		t.plainResponse(ctx, chatID, "Welcome back. Type /help to see what I can do.")
		return
	}
	if code == "" {
		t.awaiting.Store(userID, struct{}{})
		t.plainResponse(ctx, chatID, "Hello! Please send me your invite code.")
		return
	}
	t.handleInviteSubmission(ctx, chatID, userID, messageID, username, code)
}

// handleInviteSubmission tries to redeem code; on success registers the user
// and deletes their message containing the code. On failure the user stays in
// "awaiting" state so they can retype.
func (t *TgBot) handleInviteSubmission(ctx context.Context, chatID, userID int64, messageID int, username, code string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		t.plainResponse(ctx, chatID, "Please send your invite code.")
		return
	}
	if err := t.redeemAndRegister(ctx, userID, username, code); err != nil {
		t.log.With(slog.Int64("user", userID)).Info("invite redemption failed", sl.Err(err))
		t.plainResponse(ctx, chatID, "That code is invalid or already used. Try again, or /start to restart.")
		return
	}
	t.awaiting.Delete(userID)
	t.deleteMessage(ctx, chatID, messageID)
	t.log.With(slog.Int64("user", userID), slog.String("code", code)).Info("user registered")
	t.plainResponse(ctx, chatID, "Code accepted, welcome! Type /help to get started.")
}

// deleteMessage best-effort removes a message; logs at debug if it fails
// (e.g. bot lacks delete permission in a group).
func (t *TgBot) deleteMessage(ctx context.Context, chatID int64, messageID int) {
	if messageID == 0 {
		return
	}
	if _, err := t.api.DeleteMessage(ctx, &tg.DeleteMessageParams{ChatID: chatID, MessageID: messageID}); err != nil {
		t.log.With(slog.Int64("id", chatID), slog.Int("msg", messageID)).
			Debug("delete message", sl.Err(err))
	}
}

// handleAdminCallback dispatches admin menu button taps.
func (t *TgBot) handleAdminCallback(ctx context.Context, cb *models.CallbackQuery) {
	defer t.answerCallback(ctx, cb.ID)

	if !t.isAdmin(ctx, cb.From.ID) {
		return
	}
	parts := strings.SplitN(cb.Data, ":", 2)
	msg := cb.Message.Message
	if len(parts) != 2 || msg == nil {
		return
	}
	chatID := msg.Chat.ID
	msgID := msg.ID

	switch parts[1] {
	case "gen":
		code, err := t.generateAndSaveCode(ctx, cb.From.ID)
		if err != nil {
			t.editAdminText(ctx, chatID, msgID, "Failed to generate code: "+err.Error())
			return
		}
		text := fmt.Sprintf("New invite code:\n`%s`\n\nShare it with the new user. They redeem with /start <code>.", code)
		t.editAdminMarkdown(ctx, chatID, msgID, text)
	case "list":
		text := t.formatInviteList(ctx)
		t.editAdminMarkdown(ctx, chatID, msgID, text)
	}
}

// generateAndSaveCode creates and persists a unique invite code.
func (t *TgBot) generateAndSaveCode(ctx context.Context, createdBy int64) (string, error) {
	for attempts := 0; attempts < 5; attempts++ {
		code, err := generateInviteCode()
		if err != nil {
			return "", err
		}
		existing, _ := t.invites.GetInvite(ctx, code)
		if existing != nil {
			continue
		}
		invite := &storage.InviteCode{
			Code:      code,
			CreatedBy: createdBy,
			CreatedAt: time.Now(),
		}
		if err := t.invites.SaveInvite(ctx, invite); err != nil {
			return "", err
		}
		return code, nil
	}
	return "", fmt.Errorf("could not generate unique code")
}

func (t *TgBot) formatInviteList(ctx context.Context) string {
	codes, err := t.invites.ListInvites(ctx, inviteListLimit)
	if err != nil {
		return "Failed to list codes: " + err.Error()
	}
	if len(codes) == 0 {
		return "No invite codes yet."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Last %d invite codes:\n\n", len(codes))
	for _, c := range codes {
		status := "unused"
		if c.Used() {
			status = fmt.Sprintf("used by %d on %s", c.UsedBy, c.UsedAt.Format("2006-01-02"))
		}
		fmt.Fprintf(&b, "`%s` — %s\n", c.Code, status)
	}
	return b.String()
}

func (t *TgBot) editAdminText(ctx context.Context, chatID int64, msgID int, text string) {
	if _, err := t.api.EditMessageText(ctx, &tg.EditMessageTextParams{ChatID: chatID, MessageID: msgID, Text: text}); err != nil {
		t.log.With(slog.Int64("id", chatID)).Debug("admin edit", sl.Err(err))
	}
}

func (t *TgBot) editAdminMarkdown(ctx context.Context, chatID int64, msgID int, text string) {
	if _, err := t.api.EditMessageText(ctx, &tg.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: msgID,
		Text:      text,
		ParseMode: models.ParseModeMarkdownV1,
	}); err != nil {
		// Fallback to plain text if Markdown parsing fails.
		t.editAdminText(ctx, chatID, msgID, text)
	}
}

// answerCallback clears the button's loading spinner.
func (t *TgBot) answerCallback(ctx context.Context, id string) {
	if _, err := t.api.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: id}); err != nil {
		t.log.Debug("answering callback", sl.Err(err))
	}
}
