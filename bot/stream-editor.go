package bot

import (
	"context"
	"log/slog"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"Brainy/lib/sl"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// streamEditor renders a streaming chat completion in a Telegram chat.
// In private chats it streams through sendMessageDraft (a native, temporary
// preview) and sends the final text as a regular message. In groups, where
// drafts are not available, it posts a message on the first non-empty delta
// and rewrites it on a debounced ticker to stay under the edit rate limit.
// Intermediate updates are plain text; the final text applies MarkdownV2.
type streamEditor struct {
	bot    *TgBot
	chatId int64

	// draft is read and cleared only by the loop goroutine; finalize and
	// deleteIfPosted read it after stop has joined that goroutine.
	draft   bool
	draftID int

	mu      sync.Mutex
	latest  string
	msgID   int
	stopped bool

	wakeCh chan struct{}
	stopCh chan struct{}
	doneCh chan struct{}
}

func newStreamEditor(bot *TgBot, chatId int64, private bool) *streamEditor {
	return &streamEditor{
		bot:     bot,
		chatId:  chatId,
		draft:   private && !bot.draftsUnsupported.Load(),
		draftID: rand.Intn(1<<30) + 1,
		wakeCh:  make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

func (e *streamEditor) start(ctx context.Context) {
	go e.loop(ctx)
}

func (e *streamEditor) loop(ctx context.Context) {
	defer close(e.doneCh)
	ticker := time.NewTicker(streamEditInterval)
	defer ticker.Stop()

	var lastSent string
	for {
		select {
		case <-e.wakeCh:
			// First content arrived — show it right away.
			e.flush(ctx, &lastSent)
		case <-ticker.C:
			e.flush(ctx, &lastSent)
		case <-e.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (e *streamEditor) flush(ctx context.Context, lastSent *string) {
	e.mu.Lock()
	cur := e.latest
	msgID := e.msgID
	e.mu.Unlock()

	if cur == "" || cur == *lastSent {
		return
	}

	chunks := splitMessage(cur, maxMessageLen)
	if len(chunks) == 0 {
		return
	}

	if e.draft {
		// The final message carries the full text, so the preview follows
		// the tail of a long reply.
		if err := e.sendDraft(ctx, chunks[len(chunks)-1]); err != nil {
			e.bot.log.With(slog.Int64("id", e.chatId)).Warn("stream draft, falling back to edits", sl.Err(err))
			e.draft = false
			e.bot.draftsUnsupported.Store(true)
			return
		}
		*lastSent = cur
		return
	}

	// The edited message becomes the first chunk of the final reply.
	preview := chunks[0]
	if msgID == 0 {
		sent, err := e.bot.api.SendMessage(ctx, &tg.SendMessageParams{ChatID: e.chatId, Text: preview})
		if err != nil {
			e.bot.log.With(slog.Int64("id", e.chatId)).Warn("stream initial send", sl.Err(err))
			return
		}
		e.mu.Lock()
		e.msgID = sent.ID
		e.mu.Unlock()
		*lastSent = cur
		return
	}

	if _, err := e.bot.api.EditMessageText(ctx, &tg.EditMessageTextParams{ChatID: e.chatId, MessageID: msgID, Text: preview}); err != nil {
		// Telegram returns "message is not modified" if no diff; ignore.
		e.bot.log.With(slog.Int64("id", e.chatId)).Debug("stream edit", sl.Err(err))
		return
	}
	*lastSent = cur
}

func (e *streamEditor) sendDraft(ctx context.Context, text string) error {
	_, err := e.bot.api.SendMessageDraft(ctx, &tg.SendMessageDraftParams{
		ChatID:  e.chatId,
		DraftID: strconv.Itoa(e.draftID),
		Text:    text,
	})
	return err
}

// update is the callback handed to ai.AskStream.
func (e *streamEditor) update(content string) {
	e.mu.Lock()
	wasEmpty := e.latest == ""
	e.latest = content
	e.mu.Unlock()
	if wasEmpty && content != "" {
		select {
		case e.wakeCh <- struct{}{}:
		default:
		}
	}
}

// stop halts the edit ticker and waits for the loop to exit.
func (e *streamEditor) stop() {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.stopped = true
	e.mu.Unlock()
	close(e.stopCh)
	<-e.doneCh
}

// finalize delivers the formatted final text, replacing the streamed preview.
func (e *streamEditor) finalize(ctx context.Context, finalText string) {
	e.mu.Lock()
	msgID := e.msgID
	e.mu.Unlock()

	if msgID == 0 {
		// Drafts are never persisted, and in edit mode no chunk may have
		// arrived yet; either way the reply goes out as fresh messages.
		e.bot.plainResponse(ctx, e.chatId, finalText)
		return
	}

	chunks := splitMessage(finalText, maxMessageLen)
	if len(chunks) == 0 {
		return
	}
	e.editFormatted(ctx, msgID, chunks[0])
	for _, chunk := range chunks[1:] {
		e.bot.sendFormatted(ctx, e.chatId, chunk)
	}
}

func (e *streamEditor) editFormatted(ctx context.Context, msgID int, text string) {
	formatted := strings.NewReplacer("**", "*", "![", "[").Replace(text)
	sanitized := sanitize(formatted)

	if _, err := e.bot.api.EditMessageText(ctx, &tg.EditMessageTextParams{
		ChatID:    e.chatId,
		MessageID: msgID,
		Text:      sanitized,
		ParseMode: models.ParseModeMarkdown,
	}); err != nil {
		if isNotModified(err) {
			return
		}
		// Markdown failed; retry without parse mode and strip leftover markers
		// so the user doesn't see raw "**" / "*" / "_".
		if _, err2 := e.bot.api.EditMessageText(ctx, &tg.EditMessageTextParams{
			ChatID:    e.chatId,
			MessageID: msgID,
			Text:      stripMarkdown(text),
		}); err2 != nil && !isNotModified(err2) {
			e.bot.log.With(slog.Int64("id", e.chatId)).Warn("stream finalize", sl.Err(err2))
		}
	}
}

func isNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

// deleteIfPosted removes the streamed message if one was posted.
// Used when the model decided on a tool call (image) or errored out.
// A draft needs no cleanup: it is replaced by the next message or expires.
func (e *streamEditor) deleteIfPosted(ctx context.Context) {
	e.mu.Lock()
	msgID := e.msgID
	e.mu.Unlock()

	if msgID == 0 {
		return
	}
	if _, err := e.bot.api.DeleteMessage(ctx, &tg.DeleteMessageParams{ChatID: e.chatId, MessageID: msgID}); err != nil {
		e.bot.log.With(slog.Int64("id", e.chatId)).Debug("stream delete", sl.Err(err))
	}
}
