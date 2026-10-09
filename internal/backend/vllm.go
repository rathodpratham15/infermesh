package backend

import "time"

// VLLM talks to a vLLM server through its OpenAI-compatible API. Any server that
// speaks the same API (for example mlx_lm.server on Apple Silicon or llama.cpp)
// works too, which is how local development runs without an NVIDIA GPU.
type VLLM struct{ *openAIBackend }

// NewVLLM returns a backend for the server at baseURL. If model is set it
// overrides the model named in each request. timeout bounds each request
// (including a whole stream). Health is GET /v1/models, which vLLM and the
// compatible servers all expose; streaming requests ask for token usage.
func NewVLLM(name, baseURL, model string, timeout time.Duration) *VLLM {
	return &VLLM{newOpenAIBackend(name, baseURL, model, timeout, "/v1/models", true)}
}

// WithStreamUsage sets whether streaming requests ask the server for token
// usage (stream_options.include_usage). It returns the receiver for chaining.
func (v *VLLM) WithStreamUsage(on bool) *VLLM {
	v.streamUsage = on
	return v
}
