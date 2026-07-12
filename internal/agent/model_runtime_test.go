package agent

import (
	"testing"

	"aiops/internal/ai"
)

func TestAgentCanSwapClientAtRuntime(t *testing.T) {
	first := ai.NewClient("https://example.com/v1", "key-1", "model-a", 1024, 30, 0)
	second := ai.NewClient("https://example.com/v1", "key-2", "model-b", 2048, 60, 0.2)

	ag := New(first, nil, nil)
	if !ag.Ready() {
		t.Fatal("初始化传入 client 后 Agent 应为就绪")
	}
	if got, want := ag.ActiveModelName(), "model-a"; got != want {
		t.Fatalf("初始模型不正确: got %q want %q", got, want)
	}

	ag.SetClient(second)
	if got, want := ag.ActiveModelName(), "model-b"; got != want {
		t.Fatalf("切换后的模型不正确: got %q want %q", got, want)
	}
}
