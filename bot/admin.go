package bot

import (
	"Brainy/lib/sl"
	"Brainy/storage"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
func adminMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Generate code", adminCallbackPrefix+":gen"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("List codes", adminCallbackPrefix+":list"),
		),
	)
}

// isAdmin reports whether userId currently has admin role.
func (t *TgBot) isAdmin(userId int64) bool {
	if t.users == nil {
		return false
	}
	u, err := t.users.GetUser(userId)
	if err != nil || u == nil {
		return false
	}
	return u.Role == storage.RoleAdmin
}

// isAuthorized reports whether userId is registered (any role).
func (t *TgBot) isAuthorized(userId int64) bool {
	if t.users == nil {
		return true // gating disabled if users storage not wired
	}
	u, err := t.users.GetUser(userId)
	return err == nil && u != nil
}

// seedAdmins ensures every config-listed user is registered as admin at startup.
func (t *TgBot) seedAdmins(ids []int64) {
	if t.users == nil {
		return
	}
	for _, id := range ids {
		existing, _ := t.users.GetUser(id)
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
		if err := t.users.SaveUser(u); err != nil {
			t.log.With(slog.Int64("user", id)).Error("seeding admin", sl.Err(err))
		} else {
			t.log.With(slog.Int64("user", id)).Info("seeded admin")
		}
	}
}

// redeemAndRegister redeems code for a new user and persists their record.
func (t *TgBot) redeemAndRegister(userId int64, username, code string) error {
	if t.invites == nil || t.users == nil {
		return fmt.Errorf("invites disabled")
	}
	ok, err := t.invites.RedeemInvite(code, userId)
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
	return t.users.SaveUser(u)
}

// handleStart processes /start. With an arg it redeems immediately; without
// one it prompts the user for their invite code (consumed by the next message).
func (t *TgBot) handleStart(chatID, userID int64, username, code string) {
	if t.isAuthorized(userID) {
		t.plainResponse(chatID, "Welcome back. Type /help to see what I can do.")
		return
	}
	if code == "" {
		t.awaiting.Store(userID, struct{}{})
		t.plainResponse(chatID, "Hello! Please send me your invite code.")
		return
	}
	t.handleInviteSubmission(chatID, userID, username, code)
}

// handleInviteSubmission tries to redeem code; on success registers the user.
// On failure the user stays in "awaiting" state so they can retype.
func (t *TgBot) handleInviteSubmission(chatID, userID int64, username, code string) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		t.plainResponse(chatID, "Please send your invite code.")
		return
	}
	if err := t.redeemAndRegister(userID, username, code); err != nil {
		t.log.With(slog.Int64("user", userID)).Info("invite redemption failed", sl.Err(err))
		t.plainResponse(chatID, "That code is invalid or already used. Try again, or /start to restart.")
		return
	}
	t.awaiting.Delete(userID)
	t.log.With(slog.Int64("user", userID), slog.String("code", code)).Info("user registered")
	t.plainResponse(chatID, "Code accepted, welcome! Type /help to get started.")
}

// handleAdminCallback dispatches admin menu button taps.
func (t *TgBot) handleAdminCallback(cb *tgbotapi.CallbackQuery) {
	defer func() {
		if _, err := t.api.Request(tgbotapi.NewCallback(cb.ID, "")); err != nil {
			t.log.Debug("answering admin callback", sl.Err(err))
		}
	}()

	if !t.isAdmin(cb.From.ID) {
		return
	}
	parts := strings.SplitN(cb.Data, ":", 2)
	if len(parts) != 2 {
		return
	}
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	switch parts[1] {
	case "gen":
		code, err := t.generateAndSaveCode(cb.From.ID)
		if err != nil {
			t.editAdminText(chatID, msgID, "Failed to generate code: "+err.Error())
			return
		}
		text := fmt.Sprintf("New invite code:\n`%s`\n\nShare it with the new user. They redeem with /start <code>.", code)
		t.editAdminMarkdown(chatID, msgID, text)
	case "list":
		text := t.formatInviteList()
		t.editAdminMarkdown(chatID, msgID, text)
	}
}

// generateAndSaveCode creates and persists a unique invite code.
func (t *TgBot) generateAndSaveCode(createdBy int64) (string, error) {
	for attempts := 0; attempts < 5; attempts++ {
		code, err := generateInviteCode()
		if err != nil {
			return "", err
		}
		existing, _ := t.invites.GetInvite(code)
		if existing != nil {
			continue
		}
		invite := &storage.InviteCode{
			Code:      code,
			CreatedBy: createdBy,
			CreatedAt: time.Now(),
		}
		if err := t.invites.SaveInvite(invite); err != nil {
			return "", err
		}
		return code, nil
	}
	return "", fmt.Errorf("could not generate unique code")
}

func (t *TgBot) formatInviteList() string {
	codes, err := t.invites.ListInvites(inviteListLimit)
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

func (t *TgBot) editAdminText(chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	if _, err := t.api.Send(edit); err != nil {
		t.log.With(slog.Int64("id", chatID)).Debug("admin edit", sl.Err(err))
	}
}

func (t *TgBot) editAdminMarkdown(chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "Markdown"
	if _, err := t.api.Send(edit); err != nil {
		// Fallback to plain text if Markdown parsing fails.
		t.editAdminText(chatID, msgID, text)
	}
}
