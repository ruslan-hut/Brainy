package core

type ChatService interface {
	GetResponse(userId int64, prompt string) (string, error)
	GenerateImage(userId int64, prompt string) (string, error)
	DetectImageIntent(question string) (bool, string)
	ClearContext(userId int64)
}
