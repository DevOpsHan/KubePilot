package server

import (
	"testing"

	"aiops/internal/store"
)

// TestParseSuggestedAction 验证从诊断文本解析并剥离「建议动作」行。
func TestParseSuggestedAction(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		wantAction string
		wantClean  string
	}{
		{
			name:       "标准半角冒号",
			input:      "【根因分析】镜像拉取失败\n【修复建议】检查镜像名\n建议动作: restart_deployment",
			wantAction: "restart_deployment",
			wantClean:  "【根因分析】镜像拉取失败\n【修复建议】检查镜像名",
		},
		{
			name:       "全角冒号+markdown 强调",
			input:      "诊断内容\n**建议动作:** restart_pod",
			wantAction: "restart_pod",
			wantClean:  "诊断内容",
		},
		{
			name:       "none 不宜自动修复",
			input:      "根因是配置错误\n建议动作: none",
			wantAction: "none",
			wantClean:  "根因是配置错误",
		},
		{
			name:       "白名单外的值被丢弃但仍剥离该行",
			input:      "诊断\n建议动作: scale_deployment",
			wantAction: "",
			wantClean:  "诊断",
		},
		{
			name:       "带尾随说明只取第一个词",
			input:      "诊断\n建议动作: restart_pod (删除后重建)",
			wantAction: "restart_pod",
			wantClean:  "诊断",
		},
		{
			name:       "无建议动作行原样保留",
			input:      "只有诊断内容没有建议",
			wantAction: "",
			wantClean:  "只有诊断内容没有建议",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean, action := parseSuggestedAction(tc.input)
			if action != tc.wantAction {
				t.Errorf("action = %q, want %q", action, tc.wantAction)
			}
			if clean != tc.wantClean {
				t.Errorf("clean = %q, want %q", clean, tc.wantClean)
			}
		})
	}
}

// TestResolveRemediationAction 验证动作来源优先级:AI 建议 > 规则映射。
func TestResolveRemediationAction(t *testing.T) {
	s := &Server{}

	// AI 建议在白名单内 → 采用建议,bySuggestion=true
	a := &store.Alert{Rule: "PodCrashLoopBackOff", SuggestedAction: "restart_deployment"}
	action, _, _, bySuggestion, ok := s.resolveRemediationAction(a)
	if !ok || action != "restart_deployment" || !bySuggestion {
		t.Fatalf("期望采用 AI 建议 restart_deployment,实际 action=%s bySuggestion=%v ok=%v", action, bySuggestion, ok)
	}

	// AI 建议 none → 回退规则映射(此规则有默认动作)
	a = &store.Alert{Rule: "PodCrashLoopBackOff", SuggestedAction: "none"}
	action, _, _, bySuggestion, ok = s.resolveRemediationAction(a)
	if !ok || action != "restart_pod" || bySuggestion {
		t.Fatalf("none 应回退规则映射 restart_pod,实际 action=%s bySuggestion=%v ok=%v", action, bySuggestion, ok)
	}

	// 无建议 + 规则无默认动作 → 不可修复
	a = &store.Alert{Rule: "NodeHighCPU"}
	if _, _, _, _, ok = s.resolveRemediationAction(a); ok {
		t.Fatalf("NodeHighCPU 无默认修复动作,期望 ok=false")
	}
}
