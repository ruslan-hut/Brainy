package bot

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestParseCommand(t *testing.T) {
	cmdEntity := func(n int) []models.MessageEntity {
		return []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: n}}
	}
	tests := []struct {
		name     string
		msg      models.Message
		wantCmd  string
		wantArgs string
		wantOK   bool
	}{
		{"plain text", models.Message{Text: "hello"}, "", "", false},
		{"bare command", models.Message{Text: "/help", Entities: cmdEntity(5)}, "help", "", true},
		{"with args", models.Message{Text: "/ask  why is the sky blue ", Entities: cmdEntity(4)}, "ask", "why is the sky blue", true},
		{"addressed to bot", models.Message{Text: "/cat@BrainyBot poma", Entities: cmdEntity(14)}, "cat", "poma", true},
		{"unicode args", models.Message{Text: "/topic космос 🚀", Entities: cmdEntity(6)}, "topic", "космос 🚀", true},
		{"command not first", models.Message{Text: "see /help", Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 4, Length: 5}}}, "", "", false},
		{"bad length", models.Message{Text: "/x", Entities: cmdEntity(10)}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args, ok := parseCommand(&tt.msg)
			if cmd != tt.wantCmd || args != tt.wantArgs || ok != tt.wantOK {
				t.Errorf("got (%q, %q, %v), want (%q, %q, %v)", cmd, args, ok, tt.wantCmd, tt.wantArgs, tt.wantOK)
			}
		})
	}
}
