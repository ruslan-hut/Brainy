package core

type ChatService interface {
	GetResponse(userId int64, prompt string) (string, error)
	ClearContext(userId int64)
}
