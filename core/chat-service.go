package core

// Response is the outcome of a chat turn. Either Text is set (normal reply)
// or ImagePrompt is set (the model decided the user wants an image).
type Response struct {
	Text        string
	ImagePrompt string
}

type ChatService interface {
	Ask(userId int64, text string) (Response, error)
	// AskStream is like Ask but invokes onDelta with the cumulative response
	// text every time the model emits a content chunk. onDelta is not called
	// when the model returns a tool call instead of text.
	AskStream(userId int64, text string, onDelta func(content string)) (Response, error)
	OneShot(prompt string) (string, error)
	Translate(language, word, responseLanguage string) (string, error)
	GenerateImage(userId int64, prompt string) ([]byte, error)
	SetTopic(userId int64, topic string)
	GetTopic(userId int64) string
	ClearContext(userId int64)
}
