package agent

import (
	"strings"
	"testing"
)

func TestSystemPromptRequiresClusterSegmentedAnswers(t *testing.T) {
	if !strings.Contains(systemPrompt, "按集群分段") {
		t.Fatal("系统提示词应要求多集群结果按集群分段回答")
	}
}

func TestSystemPromptFollowsLatestUserLanguage(t *testing.T) {
	if strings.Contains(systemPrompt, "回答使用中文") {
		t.Fatal("系统提示词不应再把所有回答固定为中文")
	}

	requiredRules := []string{
		"最新一条用户消息",
		"主要自然语言",
		"纯英文问题使用英文回复",
		"纯中文问题使用中文回复",
		"中英文混合",
		"Kubernetes 名称、资源标识、命令、代码、日志、YAML、PromQL",
		"沿用最近一条可判断的用户消息语言",
		"默认使用中文",
		"工具返回内容保持原文",
	}
	for _, rule := range requiredRules {
		if !strings.Contains(systemPrompt, rule) {
			t.Fatalf("系统提示词缺少回复语言规则 %q", rule)
		}
	}
}
