package agent

import (
	"fmt"
	"strings"
)

// diagnosticCommandWhitelist 是 AI 在 Pod 内可执行的只读诊断命令白名单(按首 token)。
// 原则:仅允许查看类命令,不含任何写/改/删/网络外联/包管理/交互 shell。
// 命令以 argv 数组直传(不经 shell 拼接),从根本上杜绝命令拼接注入。
var diagnosticCommandWhitelist = map[string]struct{}{
	"cat":      {}, // 查看文件内容(路径由模型给出,secret 挂载路径的读取风险由使用者知晓)
	"ls":       {}, // 列目录
	"ps":       {}, // 进程列表
	"env":      {}, // 环境变量
	"printenv": {},
	"df":       {}, // 磁盘占用
	"du":       {}, // 目录占用
	"free":     {}, // 内存
	"head":     {}, // 文件头部
	"tail":     {}, // 文件尾部
	"wc":       {}, // 行/字数统计
	"stat":     {}, // 文件元信息
	"pwd":      {}, // 当前目录
	"whoami":   {}, // 当前用户
	"id":       {}, // 用户/组
	"hostname": {}, // 主机名
	"uname":    {}, // 内核信息
	"uptime":   {}, // 运行时长
	"date":     {}, // 时间
	"nslookup": {}, // DNS 解析(排查服务发现,不发业务数据)
	"getent":   {}, // NSS 查询(hosts/services 等)
	"grep":     {}, // 过滤(配合 cat 常用;本身只读)
	"find":     {}, // 查找文件(只列不改)
	"echo":     {}, // 回显(常用于查看变量)
	"cut":      {},
	"sort":     {},
	"uniq":     {},
	"nproc":    {}, // CPU 核数
	"mount":    {}, // 查看挂载(只读列出)
	"netstat":  {}, // 网络连接查看
	"ss":       {}, // socket 统计
	"ip":       {}, // 网络配置查看(ip addr / ip route,写子命令另行拦截)
}

// shellMetaChars 是禁止出现在任意参数中的 shell 元字符:
// 一旦出现说明模型试图拼接管道/重定向/子命令,直接拒绝。
const shellMetaChars = ";&|`$><\n\r"

// dangerousIPSubcommands 是 ip 命令中会修改网络配置的写子命令。
var dangerousIPSubcommands = map[string]struct{}{
	"add": {}, "del": {}, "delete": {}, "set": {}, "change": {},
	"replace": {}, "flush": {}, "up": {}, "down": {},
}

// ValidateDiagnosticCommand 校验 AI 诊断命令是否为白名单内的只读命令。
// 通过返回 nil;拒绝返回明确的中文原因(该原因会作为工具结果回给模型,让其向用户解释)。
func ValidateDiagnosticCommand(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("诊断命令不能为空")
	}
	head := strings.TrimSpace(command[0])
	if head == "" {
		return fmt.Errorf("诊断命令首个参数为空")
	}
	// 拒绝带路径的可执行(如 /bin/sh、../x),只允许纯命令名走白名单。
	if strings.ContainsAny(head, "/\\") {
		return fmt.Errorf("出于安全,诊断命令不允许指定可执行文件路径:%q", head)
	}
	if _, ok := diagnosticCommandWhitelist[head]; !ok {
		return fmt.Errorf("命令 %q 不在只读诊断白名单内,AI 只能执行查看类命令(如 cat/ls/ps/env/df/tail 等),写操作请走修复审批", head)
	}
	// 逐个参数拒绝 shell 元字符,杜绝管道/重定向/子命令拼接。
	for _, arg := range command {
		if strings.ContainsAny(arg, shellMetaChars) {
			return fmt.Errorf("诊断命令参数包含禁止的 shell 元字符(; & | ` $ > < 等):%q", arg)
		}
	}
	// ip 命令仅允许只读子命令(ip addr / ip route / ip link show 等),拦截写子命令。
	if head == "ip" {
		for _, arg := range command[1:] {
			if _, bad := dangerousIPSubcommands[strings.ToLower(strings.TrimSpace(arg))]; bad {
				return fmt.Errorf("ip 命令只允许查看类子命令,拒绝会修改网络配置的子命令:%q", arg)
			}
		}
	}
	return nil
}
