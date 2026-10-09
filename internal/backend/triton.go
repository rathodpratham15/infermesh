package backend

import "time"

// Triton talks to NVIDIA Triton Inference Server through Triton's
// OpenAI-compatible frontend (the documented path for TensorRT-LLM models,
// served as /v1/chat/completions), so it presents the same wire contract as vLLM
// and the gateway can route between them without knowing which is which.
//
// It deliberately does not use Triton's native generate extension: that is
// marked provisional and its TensorRT-LLM models require engine-specific fields
// (bad_words, stop_words), which would leak engine details into the gateway.
//
// Differences from vLLM, from NVIDIA's documentation:
//   - readiness is GET /health/ready (vLLM and the stand-ins use /v1/models);
//   - streaming and stream_options are not documented for the TensorRT-LLM
//     path, so usage-in-stream is OFF unless streamUsage is enabled. Without it,
//     streamed responses carry no token counts (the gateway reports that as
//     missing usage rather than guessing).
//
// This backend is exercised against a fake Triton server in tests. It has not
// been run against a real Triton + TensorRT-LLM deployment, which needs an
// NVIDIA GPU.
type Triton struct{ *openAIBackend }

func NewTriton(name, baseURL, model string, timeout time.Duration, streamUsage bool) *Triton {
	return &Triton{newOpenAIBackend(name, baseURL, model, timeout, "/health/ready", streamUsage)}
}
