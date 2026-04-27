package bot

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"Brainy/lib/sl"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
)

// streamEditor renders a streaming chat completion to a single Telegram
// message. It posts the message on the first non-empty delta and rewrites it
// on a debounced ticker (~1/sec) to stay under Telegram's edit rate limit.
// Intermediate edits are sent as plain text; the final edit applies MarkdownV2.
type streamEditor struct {
	bot    *TgBot
	chatId int64

	mu      sync.Mutex
	latest  string
	msgID   int
	stopped bool

	stopCh chan struct{}
	doneCh chan struct{}
}

func newStreamEditor(bot *TgBot, chatId int64) *streamEditor {
	return &streamEditor{
		bot:    bot,
		chatId: chatId,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (e *streamEditor) start() {
	go e.loop()
}

func (e *streamEditor) loop() {
	defer close(e.doneCh)
	ticker := time.NewTicker(streamEditInterval)
	defer ticker.Stop()

	var lastSent string
	for {
		select {
		case <-ticker.C:
			e.flush(&lastSent)
		case <-e.stopCh:
			return
		}
	}
}

func (e *streamEditor) flush(lastSent *string) {
	e.mu.Lock()
	cur := e.latest
	msgID := e.msgID
	e.mu.Unlock()

	if cur == "" || cur == *lastSent {
		return
	}

	if msgID == 0 {
		sent, err := e.bot.api.Send(tgbotapi.NewMessage(e.chatId, cur))
		if err != nil {
			e.bot.log.With(slog.Int64("id", e.chatId)).Warn("stream initial send", sl.Err(err))
			return
		}
		e.mu.Lock()
		e.msgID = sent.MessageID
		e.mu.Unlock()
		*lastSent = cur
		return
	}

	edit := tgbotapi.NewEditMessageText(e.chatId, msgID, cur)
	if _, err := e.bot.api.Send(edit); err != nil {
		// Telegram returns "message is not modified" if no diff; ignore.
		e.bot.log.With(slog.Int64("id", e.chatId)).Debug("stream edit", sl.Err(err))
		return
	}
	*lastSent = cur
}

// update is the callback handed to ai.AskStream.
func (e *streamEditor) update(content string) {
	e.mu.Lock()
	e.latest = content
	e.mu.Unlock()
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

// finalize replaces the streamed message with the formatted final text.
func (e *streamEditor) finalize(finalText string) {
	e.mu.Lock()
	msgID := e.msgID
	e.mu.Unlock()

	if msgID == 0 {
		// No streaming chunks ever arrived; send a fresh message.
		e.bot.plainResponse(e.chatId, finalText)
		return
	}

	formatted := strings.NewReplacer("**", "*", "![", "[").Replace(finalText)
	sanitized := sanitize(formatted)

	edit := tgbotapi.NewEditMessageText(e.chatId, msgID, sanitized)
	edit.ParseMode = "MarkdownV2"
	if _, err := e.bot.api.Send(edit); err != nil {
		// Markdown failed; retry without parse mode.
		fallback := tgbotapi.NewEditMessageText(e.chatId, msgID, finalText)
		if _, err2 := e.bot.api.Send(fallback); err2 != nil {
			e.bot.log.With(slog.Int64("id", e.chatId)).Warn("stream finalize", sl.Err(err2))
		}
	}
}

// deleteIfPosted removes the streaming placeholder if one was posted.
// Used when the model decided on a tool call (image) or errored out.
func (e *streamEditor) deleteIfPosted() {
	e.mu.Lock()
	msgID := e.msgID
	e.mu.Unlock()

	if msgID == 0 {
		return
	}
	if _, err := e.bot.api.Send(tgbotapi.NewDeleteMessage(e.chatId, msgID)); err != nil {
		e.bot.log.With(slog.Int64("id", e.chatId)).Debug("stream delete", sl.Err(err))
	}
}
