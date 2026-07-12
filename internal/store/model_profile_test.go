package store

import (
	"path/filepath"
	"testing"
)

func TestModelProfileDefaultIsUnique(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	first, err := st.CreateModelProfile(CreateModelProfileInput{
		Name:            "DeepSeek",
		Provider:        "openai",
		BaseURL:         "https://api.deepseek.com",
		ModelName:       "deepseek-chat",
		EncryptedAPIKey: "encrypted-1",
		IsDefault:       true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建第一个模型失败: %v", err)
	}
	second, err := st.CreateModelProfile(CreateModelProfileInput{
		Name:            "Qwen",
		Provider:        "openai",
		BaseURL:         "https://dashscope.aliyuncs.com/compatible-mode/v1",
		ModelName:       "qwen-plus",
		EncryptedAPIKey: "encrypted-2",
		IsDefault:       true,
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建第二个模型失败: %v", err)
	}

	defaultProfile, err := st.GetDefaultModelProfile()
	if err != nil {
		t.Fatalf("查询默认模型失败: %v", err)
	}
	if defaultProfile.ID != second.ID {
		t.Fatalf("默认模型应切换到第二个: got %d want %d", defaultProfile.ID, second.ID)
	}

	updatedFirst, err := st.GetModelProfile(first.ID)
	if err != nil {
		t.Fatalf("查询第一个模型失败: %v", err)
	}
	if updatedFirst.IsDefault {
		t.Fatalf("第一个模型不应继续保持默认: %+v", updatedFirst)
	}
}

func TestFirstModelProfileBecomesDefault(t *testing.T) {
	st, err := Open("sqlite", filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	defer func() { _ = st.Close() }()

	profile, err := st.CreateModelProfile(CreateModelProfileInput{
		Name:            "默认模型",
		Provider:        "openai",
		BaseURL:         "https://example.com/v1",
		ModelName:       "ops-model",
		EncryptedAPIKey: "encrypted",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("创建模型失败: %v", err)
	}
	if !profile.IsDefault {
		t.Fatalf("第一条模型配置应自动成为默认模型: %+v", profile)
	}
}
