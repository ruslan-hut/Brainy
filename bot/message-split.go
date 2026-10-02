package bot

import (
	"strings"
	"unicode/utf16"
)

// maxMessageLen is Telegram's text limit, counted in UTF-16 code units.
const maxMessageLen = 4096

const codeFence = "```"

// splitMessage breaks text into non-empty chunks of at most limit UTF-16
// units, preferring paragraph, line and word boundaries. A code block cut in
// two is closed at the end of one chunk and reopened at the start of the next
// so each chunk parses as MarkdownV2 on its own. The limit applies to the raw
// text: MarkdownV2 escapes don't count, as Telegram measures after parsing.
func splitMessage(text string, limit int) []string {
	var chunks []string
	inFence := false
	for {
		prefix := ""
		if inFence {
			prefix = codeFence + "\n"
		}
		if utf16Len(prefix)+utf16Len(text) <= limit {
			if strings.TrimSpace(text) != "" {
				chunks = append(chunks, prefix+text)
			}
			return chunks
		}

		budget := limit - utf16Len(prefix) - utf16Len("\n"+codeFence)
		cut := cutPoint(text, budget)
		body := strings.TrimRight(text[:cut], " \n")
		text = text[cut:]

		inFence = strings.Count(prefix+body, codeFence)%2 == 1
		// Leading spaces are code indentation inside a fence; keep them.
		if inFence {
			text = strings.TrimLeft(text, "\n")
		} else {
			text = strings.TrimLeft(text, " \n")
		}

		if strings.TrimSpace(body) == "" {
			continue
		}
		chunk := prefix + body
		if inFence {
			chunk += "\n" + codeFence
		}
		chunks = append(chunks, chunk)
	}
}

// cutPoint returns a byte offset into text where a chunk of at most budget
// UTF-16 units should end.
func cutPoint(text string, budget int) int {
	end, n := 0, 0
	for i, r := range text {
		n += utf16RuneLen(r)
		if n > budget {
			break
		}
		end = i + len(string(r))
	}
	head := text[:end]
	for _, sep := range []string{"\n\n", "\n", " "} {
		if i := strings.LastIndex(head, sep); i > end/2 {
			return i
		}
	}
	// Hard cut: don't split a run of backticks, or fence counting breaks.
	if i := len(strings.TrimRight(head, "`")); i > 0 {
		return i
	}
	return end
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16RuneLen(r)
	}
	return n
}

func utf16RuneLen(r rune) int {
	if l := utf16.RuneLen(r); l > 0 {
		return l
	}
	return 1
}
