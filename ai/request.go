package ai

type GPTRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func NewRequest(content string) *GPTRequest {
	return &GPTRequest{
		Model:       "gpt-4o-mini",
		Messages:    []Message{{Role: "user", Content: content}},
		Temperature: 0.7,
	}
}
