package ai

import (
	"Brainy/core"
	"Brainy/storage"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeOpenAI answers every chat completion with a fixed preferences analysis
// and records the request bodies it received.
func fakeOpenAI(t *testing.T) (calls *atomic.Int32, bodies chan string) {
	t.Helper()
	calls = &atomic.Int32{}
	bodies = make(chan string, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		bodies <- string(body)
		content, _ := json.Marshal(storage.PreferencesAnalysis{PreferredLanguage: "Ukrainian", Formality: "informal"})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "test", "object": "chat.completion", "created": 0, "model": "test",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": string(content)},
			}},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("OPENAI_BASE_URL", srv.URL+"/")
	return calls, bodies
}

func TestClearContextAnalyzesBeforeClearing(t *testing.T) {
	const user = int64(1)
	tests := []struct {
		name         string
		userMessages int
		manuallySet  bool
		wantAnalyzed bool
	}{
		{"enough history", 3, false, true},
		{"too little history", 2, false, false},
		{"preferences set manually", 3, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls, bodies := fakeOpenAI(t)
			ctx := t.Context()
			conf := &core.Config{Model: "test", OpenAIApiKey: "test"}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			dialogs := storage.NewMemoryStorage()
			prefs := storage.NewMemoryPreferencesStorage()

			for i := range tt.userMessages {
				_ = dialogs.UpdateUserContext(ctx, user, storage.Message{IsUser: true, Text: "question " + string(rune('A'+i))})
				_ = dialogs.UpdateUserContext(ctx, user, storage.Message{Text: "answer"})
			}
			if tt.manuallySet {
				_ = prefs.SaveUserPreferences(ctx, &storage.UserPreferences{UserId: user, ManuallySet: true})
			}

			chat := NewChat(conf, log, dialogs)
			analyzer := NewPreferencesAnalyzer(conf, log, dialogs, prefs)
			chat.SetPreferencesAnalyzer(analyzer)

			chat.ClearContext(ctx, user)
			analyzer.Wait()

			if got, _ := dialogs.GetUserContext(ctx, user); got != nil {
				t.Errorf("context not cleared: %+v", got)
			}
			if analyzed := calls.Load() > 0; analyzed != tt.wantAnalyzed {
				t.Fatalf("analyzed = %v, want %v", analyzed, tt.wantAnalyzed)
			}
			if !tt.wantAnalyzed {
				return
			}
			if body := <-bodies; !strings.Contains(body, "question A") || !strings.Contains(body, "question C") {
				t.Errorf("analysis request missing dialog messages: %s", body)
			}
			got, err := prefs.GetUserPreferences(ctx, user)
			if err != nil || got == nil || got.PreferredLanguage != "Ukrainian" {
				t.Errorf("preferences not saved: %+v, %v", got, err)
			}
		})
	}
}
