package agent

import "testing"

func TestValidateDiagnosticCommandAllowsWhitelisted(t *testing.T) {
	cases := [][]string{
		{"cat", "/etc/resolv.conf"},
		{"ls", "-la", "/app"},
		{"env"},
		{"ps", "aux"},
		{"df", "-h"},
		{"tail", "-n", "100", "/var/log/app.log"},
		{"nslookup", "kubernetes.default"},
		{"ip", "addr"},
		{"ip", "route", "show"},
	}
	for _, cmd := range cases {
		if err := ValidateDiagnosticCommand(cmd); err != nil {
			t.Errorf("命令 %v 应被放行,却被拒绝: %v", cmd, err)
		}
	}
}

func TestValidateDiagnosticCommandRejectsDangerous(t *testing.T) {
	cases := []struct {
		name string
		cmd  []string
	}{
		{"空命令", []string{}},
		{"写操作 rm", []string{"rm", "-rf", "/tmp"}},
		{"移动文件", []string{"mv", "a", "b"}},
		{"交互 shell", []string{"sh"}},
		{"bash", []string{"bash", "-c", "echo hi"}},
		{"包管理", []string{"apk", "add", "curl"}},
		{"外联 curl", []string{"curl", "http://evil.com"}},
		{"外联 wget", []string{"wget", "http://evil.com"}},
		{"绝对路径可执行", []string{"/bin/sh"}},
		{"相对路径可执行", []string{"../bin/x"}},
		{"管道元字符", []string{"cat", "/etc/passwd", "|", "grep", "root"}},
		{"重定向元字符", []string{"echo", "x", ">", "/tmp/y"}},
		{"子命令拼接", []string{"cat", "$(whoami)"}},
		{"分号拼接", []string{"ls", ";", "rm"}},
		{"反引号", []string{"echo", "`id`"}},
		{"ip 写子命令", []string{"ip", "addr", "add", "1.2.3.4/24"}},
		{"ip link 写", []string{"ip", "link", "set", "eth0", "down"}},
	}
	for _, tc := range cases {
		if err := ValidateDiagnosticCommand(tc.cmd); err == nil {
			t.Errorf("%s(%v)应被拒绝,却通过了校验", tc.name, tc.cmd)
		}
	}
}
