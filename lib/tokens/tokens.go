// Package tokens provides accurate token counting for OpenAI models.
//
// Init must be called once at startup with the target model name. Count is
// safe to call before Init — it falls back to a 4-chars-per-token heuristic.
package tokens

import (
	"strings"
	"sync/atomic"

	"github.com/pkoukk/tiktoken-go"
)

var encoder atomic.Pointer[tiktoken.Tiktoken]

// Init resolves the encoding for the given model and caches it for Count.
// On failure (e.g. unknown model, BPE download error) Count falls back to a
// 4-chars-per-token heuristic.
func Init(model string) error {
	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		// Try a sensible fallback for new gpt-* models that may not be in
		// the tiktoken-go model table yet.
		fallback := encodingForUnknownModel(model)
		if fallback == "" {
			return err
		}
		enc, err = tiktoken.GetEncoding(fallback)
		if err != nil {
			return err
		}
	}
	encoder.Store(enc)
	return nil
}

// Count returns the number of tokens in text. Falls back to len/4 when the
// encoder is not initialized.
func Count(text string) int {
	if text == "" {
		return 0
	}
	if enc := encoder.Load(); enc != nil {
		return len(enc.Encode(text, nil, nil))
	}
	return len(text)/4 + 1
}

func encodingForUnknownModel(model string) string {
	switch {
	case strings.HasPrefix(model, "gpt-4o"),
		strings.HasPrefix(model, "gpt-4.1"),
		strings.HasPrefix(model, "gpt-5"),
		strings.HasPrefix(model, "o1"),
		strings.HasPrefix(model, "o3"):
		return "o200k_base"
	case strings.HasPrefix(model, "gpt-4"),
		strings.HasPrefix(model, "gpt-3.5"):
		return "cl100k_base"
	}
	return ""
}
