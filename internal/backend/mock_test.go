package backend

import (
	"context"
	"testing"
	"time"
)

func TestMockGenerate(t *testing.T) {
	m := NewMock("mock", 0)
	resp, err := m.Generate(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello there"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content == "" || resp.FinishReason != "stop" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Usage.PromptTokens != 2 || resp.Usage.CompletionTokens != len(m.Tokens) {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestMockMaxTokens(t *testing.T) {
	m := NewMock("mock", 0)
	resp, err := m.Generate(context.Background(), Request{MaxTokens: 3})
	if err != nil {
		t.Fatal(err)
	}
	if resp.FinishReason != "length" || resp.Usage.CompletionTokens != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestMockCancel(t *testing.T) {
	m := NewMock("mock", 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Generate(ctx, Request{}); err == nil {
		t.Fatal("expected context error")
	}
}

func TestMockUnhealthy(t *testing.T) {
	m := NewMock("mock", 0)
	m.SetHealthy(false)
	if err := m.Health(context.Background()); err == nil {
		t.Fatal("expected unhealthy")
	}
	if _, err := m.Generate(context.Background(), Request{}); err == nil {
		t.Fatal("expected generate to fail when unhealthy")
	}
}
