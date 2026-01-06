package ai

// ImageGenerationRequest represents a request to DALL-E API
type ImageGenerationRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	N      int    `json:"n"`
	Size   string `json:"size"`
}

// ImageGenerationResponse represents the response from DALL-E API
type ImageGenerationResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
	Error   *Error      `json:"error"`
}

// ImageData represents a single generated image
type ImageData struct {
	URL           string `json:"url"`
	B64JSON       string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
}

// NewImageRequest creates a new image generation request
func NewImageRequest(prompt string) *ImageGenerationRequest {
	return &ImageGenerationRequest{
		Model:  "dall-e-3",
		Prompt: prompt,
		N:      1,
		Size:   "1024x1024",
	}
}
