package core

// Response is the outcome of a chat turn. Either Text is set (normal reply)
// or ImagePrompt is set (the model decided the user wants an image).
type Response struct {
	Text        string
	ImagePrompt string
}

type ChatService interface {
	Ask(userId int64, text string) (Response, error)
	OneShot(prompt string) (string, error)
	Translate(language, word string) (string, error)
	GenerateImage(userId int64, prompt string) ([]byte, error)
	SetTopic(userId int64, topic string)
	ClearContext(userId int64)
}
