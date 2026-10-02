package bot

import (
	"strings"
	"testing"
)

func TestSplitMessage(t *testing.T) {
	para := strings.Repeat("word ", 30) // 150 units
	code := "```\n" + strings.Repeat("line of code\n", 20) + "```"

	tests := []struct {
		name  string
		text  string
		limit int
		want  int
	}{
		{"fits", "hello", 100, 1},
		{"exact limit", strings.Repeat("a", 100), 100, 1},
		{"paragraphs", para + "\n\n" + para + "\n\n" + para, 200, 3},
		{"no separators", strings.Repeat("a", 250), 100, 3},
		{"emoji counts double", strings.Repeat("😀", 60), 100, 2},
		{"code block", "intro\n\n" + code + "\n\noutro", 120, 3},
		{"empty", "", 100, 0},
		{"whitespace tail", "text" + strings.Repeat(" ", 300), 100, 1},
		{"backticks at hard cut", strings.Repeat("a", 95) + "```" + strings.Repeat("b", 95) + "```", 100, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitMessage(tt.text, tt.limit)
			if len(chunks) != tt.want {
				t.Fatalf("got %d chunks, want %d: %q", len(chunks), tt.want, chunks)
			}
			for i, c := range chunks {
				if n := utf16Len(c); n > tt.limit {
					t.Errorf("chunk %d has %d units, limit %d", i, n, tt.limit)
				}
				if strings.Count(c, codeFence)%2 != 0 {
					t.Errorf("chunk %d has unbalanced code fence: %q", i, c)
				}
				if c == "" {
					t.Errorf("chunk %d is empty", i)
				}
			}
		})
	}
}

func TestSplitMessageKeepsCodeIndentation(t *testing.T) {
	var code strings.Builder
	code.WriteString("```\n")
	for range 20 {
		code.WriteString("    indented()\n")
	}
	code.WriteString("```")

	for i, c := range splitMessage(code.String(), 100) {
		for _, line := range strings.Split(c, "\n") {
			if line != codeFence && !strings.HasPrefix(line, "    ") {
				t.Errorf("chunk %d lost indentation: %q", i, line)
			}
		}
	}
}
