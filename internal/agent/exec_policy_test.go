package agent

import "testing"

func TestValidateDiagnosticCommandAllowsWhitelisted(t *testing.T) {
	cases := [][]string{
		{"cat", "/etc/resolv.conf"},
		{"ls", "-la", "/app"},
		{"env"},
		{"env", "-0"},
		{"ps", "aux"},
		{"df", "-h"},
		{"tail", "-n", "100", "/var/log/app.log"},
		{"nslookup", "kubernetes.default"},
		{"ip", "addr"},
		{"ip", "route", "show"},
		{"ip", "route", "get", "10.0.0.1"},
		{"ip", "-br", "addr"},
		{"date", "-u", "+%Y-%m-%dT%H:%M:%SZ"},
		{"date", "-d", "yesterday"},
		{"sort", "-t", ",", "-k", "2", "/tmp/data.csv"},
		{"sort", "-rt,", "-k2,2", "/tmp/data.csv"},
		{"sort", "-tT", "/tmp/data.csv"},
		{"hostname", "-f"},
		{"mount", "-l"},
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
		{"env 启动程序", []string{"env", "rm", "-rf", "/"}},
		{"env 启动 shell", []string{"env", "/bin/sh", "-c", "id"}},
		{"env 变量后启动程序", []string{"env", "PATH=/tmp", "malicious"}},
		{"find delete", []string{"find", "/tmp", "-delete"}},
		{"find exec plus", []string{"find", "/tmp", "-exec", "rm", "-rf", "{}", "+"}},
		{"find fprint", []string{"find", "/tmp", "-fprint", "/tmp/result"}},
		{"sort 短参数输出", []string{"sort", "-o", "/tmp/result", "/etc/hosts"}},
		{"sort 组合短参数输出", []string{"sort", "-ro/tmp/result", "/etc/hosts"}},
		{"sort 组合短参数临时目录", []string{"sort", "-rT/tmp/controlled", "/etc/hosts"}},
		{"sort 长参数输出", []string{"sort", "--output=/tmp/result", "/etc/hosts"}},
		{"sort 外部压缩程序", []string{"sort", "--compress-program=sh", "/etc/hosts"}},
		{"sort 指定临时目录", []string{"sort", "-T", "/tmp/controlled", "/etc/hosts"}},
		{"sort 缩写长参数输出", []string{"sort", "--out=/tmp/result", "/etc/hosts"}},
		{"sort 缩写外部压缩程序", []string{"sort", "--compress-prog=sh", "/etc/hosts"}},
		{"sort 缩写临时目录", []string{"sort", "--temp=/tmp/controlled", "/etc/hosts"}},
		{"date 短参数改时间", []string{"date", "-s", "2030-01-01"}},
		{"date 长参数改时间", []string{"date", "--set=2030-01-01"}},
		{"date 缩写长参数改时间", []string{"date", "--se=2030-01-01"}},
		{"tail follow", []string{"tail", "-f", "/var/log/app.log"}},
		{"tail retry follow", []string{"tail", "-F", "/var/log/app.log"}},
		{"tail 跟随进程", []string{"tail", "--pid=1", "/var/log/app.log"}},
		{"tail 缩写 follow", []string{"tail", "--fol=name", "/var/log/app.log"}},
		{"tail 缩写 retry", []string{"tail", "--ret", "--fol=name", "/var/log/app.log"}},
		{"tail 缩写 pid", []string{"tail", "--pi=1", "/var/log/app.log"}},
		{"hostname 改主机名", []string{"hostname", "new-hostname"}},
		{"hostname 从文件修改", []string{"hostname", "-F", "/tmp/name"}},
		{"mount 挂载文件系统", []string{"mount", "-t", "tmpfs", "tmpfs", "/tmp/mnt"}},
		{"mount all", []string{"mount", "--all"}},
		{"ip netns exec", []string{"ip", "netns", "exec", "demo", "sh"}},
		{"ip batch", []string{"ip", "-batch", "/tmp/commands"}},
		{"ip monitor", []string{"ip", "monitor"}},
		{"ip 未声明对象", []string{"ip", "xfrm", "state", "allocspi", "src", "10.0.0.1", "dst", "10.0.0.2"}},
		{"ss kill socket", []string{"ss", "-K", "dst", "10.0.0.1"}},
		{"ss long kill", []string{"ss", "--kill", "dst", "10.0.0.1"}},
		{"ss 缩写 kill", []string{"ss", "--kil", "dst", "10.0.0.1"}},
		{"nslookup 交互模式", []string{"nslookup"}},
		{"uniq 输出文件", []string{"uniq", "/etc/hosts", "/tmp/result"}},
	}
	for _, tc := range cases {
		if err := ValidateDiagnosticCommand(tc.cmd); err == nil {
			t.Errorf("%s(%v)应被拒绝,却通过了校验", tc.name, tc.cmd)
		}
	}
}
