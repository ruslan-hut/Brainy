package core

type ChatService interface {
	GetResponse(prompt string) (string, error)
}
