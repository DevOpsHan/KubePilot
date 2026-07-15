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
	"nslookup": {}, // 仅允许固定 Kubernetes DNS 探针目标,避免任意 DNS 外带
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
	"replace": {}, "flush": {}, "up": {}, "down": {}, "append": {},
	"prepend": {}, "restore": {}, "attach": {}, "detach": {},
	"rename": {}, "move": {}, "push": {}, "pop": {}, "reset": {},
	"exec": {}, "monitor": {}, "batch": {},
}

var dangerousFindActions = map[string]struct{}{
	"-delete":  {},
	"-exec":    {},
	"-execdir": {},
	"-ok":      {},
	"-okdir":   {},
	"-fls":     {},
	"-fprint":  {},
	"-fprint0": {},
	"-fprintf": {},
}

var safeHostnameArgs = map[string]struct{}{
	"-a": {}, "--alias": {}, "-A": {}, "--all-fqdns": {},
	"-d": {}, "--domain": {}, "-f": {}, "--fqdn": {}, "--long": {},
	"-i": {}, "--ip-address": {}, "-I": {}, "--all-ip-addresses": {},
	"-s": {}, "--short": {}, "-y": {}, "--yp": {}, "--nis": {},
	"-h": {}, "--help": {}, "-V": {}, "--version": {},
}

var safeMountArgs = map[string]struct{}{
	"-l": {}, "--show-labels": {}, "-v": {}, "--verbose": {}, "-lv": {}, "-vl": {},
}

var safeDNSProbeTargets = map[string]struct{}{
	"kubernetes.default":                   {},
	"kubernetes.default.svc":               {},
	"kubernetes.default.svc.cluster.local": {},
}

const safeSSShortOptions = "460tudwxlaempTiOonbsHhV"

var safeSSLongArgs = map[string]struct{}{
	"--ipv4": {}, "--ipv6": {}, "--packet": {}, "--tcp": {}, "--udp": {},
	"--dccp": {}, "--raw": {}, "--unix": {}, "--listening": {}, "--all": {},
	"--extended": {}, "--memory": {}, "--processes": {}, "--threads": {},
	"--info": {}, "--oneline": {}, "--options": {}, "--numeric": {}, "--bpf": {},
	"--summary": {}, "--no-header": {}, "--help": {}, "--version": {},
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

	switch head {
	case "env":
		// env 后出现普通参数时会把它当作可执行程序,因此只允许直接打印环境变量。
		for _, arg := range command[1:] {
			if arg != "-0" && arg != "--null" && arg != "--help" && arg != "--version" {
				return fmt.Errorf("env 仅允许直接查看环境变量,不允许设置变量或启动其他程序:%q", arg)
			}
		}
	case "find":
		for _, arg := range command[1:] {
			if _, bad := dangerousFindActions[strings.ToLower(strings.TrimSpace(arg))]; bad {
				return fmt.Errorf("find 仅允许查找和打印,拒绝会删除、执行程序或写文件的动作:%q", arg)
			}
		}
	case "sort":
		if err := validateReadOnlySortArgs(command[1:]); err != nil {
			return err
		}
	case "date":
		if err := validateReadOnlyDateArgs(command[1:]); err != nil {
			return err
		}
	case "tail":
		for _, arg := range command[1:] {
			lower := strings.ToLower(strings.TrimSpace(arg))
			if matchesLongOption(lower, "--retry") || matchesLongOption(lower, "--follow") ||
				matchesLongOption(lower, "--pid") ||
				strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.ContainsAny(strings.TrimPrefix(arg, "-"), "fF") {
				return fmt.Errorf("tail 仅允许一次性读取,拒绝持续跟随参数:%q", arg)
			}
		}
	case "hostname":
		for _, arg := range command[1:] {
			if _, ok := safeHostnameArgs[arg]; !ok {
				return fmt.Errorf("hostname 仅允许查看主机信息,拒绝可能修改主机名的参数:%q", arg)
			}
		}
	case "mount":
		for _, arg := range command[1:] {
			if _, ok := safeMountArgs[arg]; !ok {
				return fmt.Errorf("mount 仅允许列出当前挂载,拒绝挂载、卸载或批量操作参数:%q", arg)
			}
		}
	case "ss":
		if err := validateReadOnlySSArgs(command[1:]); err != nil {
			return err
		}
	case "nslookup":
		if len(command) != 2 {
			return fmt.Errorf("nslookup 仅允许查询一个固定 Kubernetes DNS 探针目标,不允许交互模式、选项或自定义 DNS 服务器")
		}
		if _, ok := safeDNSProbeTargets[command[1]]; !ok {
			return fmt.Errorf("nslookup 仅允许规范形式的固定 Kubernetes DNS 探针目标,拒绝任意域名、地址或变体:%q", command[1])
		}
	case "uniq":
		if err := validateReadOnlyUniqArgs(command[1:]); err != nil {
			return err
		}
	case "ip":
		if err := validateReadOnlyIPArgs(command[1:]); err != nil {
			return err
		}
	}
	return nil
}

func validateReadOnlySSArgs(args []string) error {
	for _, arg := range args {
		if arg == "" || arg != strings.TrimSpace(arg) {
			return fmt.Errorf("ss 仅允许规范形式的纯查看选项,拒绝空白或空参数:%q", arg)
		}
		if strings.HasPrefix(arg, "--") {
			if _, ok := safeSSLongArgs[arg]; !ok {
				return fmt.Errorf("ss 仅允许不会解析目标、读取过滤器、写文件或持续监听的纯查看选项:%q", arg)
			}
			continue
		}
		if !strings.HasPrefix(arg, "-") || len(arg) == 1 {
			return fmt.Errorf("ss 不允许地址、端口或表达式过滤操作数,拒绝:%q", arg)
		}
		for _, option := range arg[1:] {
			if !strings.ContainsRune(safeSSShortOptions, option) {
				return fmt.Errorf("ss 仅允许不会解析目标、读取过滤器、写文件或持续监听的纯查看选项:%q", arg)
			}
		}
	}
	return nil
}

// matchesLongOption 同时识别 GNU/getopt 风格的唯一长参数缩写。
// 例如 --out=/tmp/x 会被 sort 当成 --output=/tmp/x，安全校验也必须按
// 实际命令的解析方式处理，不能只比较完整拼写。
func matchesLongOption(argument, fullOption string) bool {
	argument = strings.ToLower(strings.TrimSpace(argument))
	fullOption = strings.ToLower(strings.TrimSpace(fullOption))
	if argument == "--" || !strings.HasPrefix(argument, "--") {
		return false
	}
	if index := strings.IndexByte(argument, '='); index >= 0 {
		argument = argument[:index]
	}
	return len(argument) > 2 && strings.HasPrefix(fullOption, argument)
}

func validateReadOnlySortArgs(args []string) error {
	optionsEnded := false
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		lower := strings.ToLower(trimmed)
		if !optionsEnded && trimmed == "--" {
			optionsEnded = true
			continue
		}
		if optionsEnded || !strings.HasPrefix(trimmed, "-") || trimmed == "-" {
			continue
		}
		if strings.HasPrefix(trimmed, "--") {
			if matchesLongOption(lower, "--output") ||
				matchesLongOption(lower, "--compress-program") ||
				matchesLongOption(lower, "--temporary-directory") {
				return fmt.Errorf("sort 仅允许把结果输出到标准输出,拒绝写文件或启动外部程序:%q", arg)
			}
			continue
		}

		// sort/BusyBox sort 允许组合短参数，例如 -ro/tmp/out 等价于
		// -r -o /tmp/out。逐个解析无值开关；遇到 k/S/t 后，其余字符
		// 是该安全选项的取值，不能把分隔符中的 T/o 误判成新选项。
		for index := 1; index < len(trimmed); index++ {
			switch trimmed[index] {
			case 'o', 'T':
				return fmt.Errorf("sort 仅允许把结果输出到标准输出,拒绝写文件或指定临时目录:%q", arg)
			case 'k', 'S', 't':
				index = len(trimmed)
			}
		}
	}
	return nil
}

func validateReadOnlyUniqArgs(args []string) error {
	operands := 0
	expectsValue := false
	optionsEnded := false
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if expectsValue {
			expectsValue = false
			continue
		}
		if !optionsEnded && trimmed == "--" {
			optionsEnded = true
			continue
		}
		if !optionsEnded {
			switch trimmed {
			case "-f", "--skip-fields", "-s", "--skip-chars", "-w", "--check-chars":
				expectsValue = true
				continue
			}
			if strings.HasPrefix(trimmed, "--skip-fields=") || strings.HasPrefix(trimmed, "--skip-chars=") ||
				strings.HasPrefix(trimmed, "--check-chars=") || strings.HasPrefix(trimmed, "--group=") {
				continue
			}
			if strings.HasPrefix(trimmed, "-") && trimmed != "-" || strings.HasPrefix(trimmed, "+") {
				continue
			}
		}
		operands++
		if operands > 1 {
			return fmt.Errorf("uniq 仅允许从标准输入或一个输入文件读取,拒绝指定输出文件:%q", arg)
		}
	}
	if expectsValue {
		return fmt.Errorf("uniq 查看参数缺少取值")
	}
	return nil
}

func validateReadOnlyIPArgs(args []string) error {
	if len(args) == 0 {
		return nil
	}
	index := 0
	for index < len(args) && strings.HasPrefix(strings.TrimSpace(args[index]), "-") {
		option := strings.TrimSpace(args[index])
		switch option {
		case "-4", "-6", "-B", "-0", "-o", "-br", "-brief", "-j", "-json", "-p", "-pretty", "-d", "-details", "-s", "-stats", "-statistics", "-h", "-human", "-iec", "-V", "-Version":
			index++
			continue
		default:
			return fmt.Errorf("ip 命令只允许常用查看参数,拒绝批处理或其他高风险全局参数:%q", args[index])
		}
	}
	if index >= len(args) {
		return nil
	}
	object := strings.ToLower(strings.TrimSpace(args[index]))
	index++
	if object == "help" {
		if index == len(args) {
			return nil
		}
		return fmt.Errorf("ip help 后不允许附加其他命令")
	}
	allowedActions := map[string]map[string]struct{}{
		"addr":     {"show": {}, "list": {}},
		"address":  {"show": {}, "list": {}},
		"link":     {"show": {}, "list": {}},
		"route":    {"show": {}, "list": {}, "get": {}},
		"neigh":    {"show": {}, "list": {}},
		"neighbor": {"show": {}, "list": {}},
		"rule":     {"show": {}, "list": {}},
	}
	actions, ok := allowedActions[object]
	if !ok {
		return fmt.Errorf("ip 仅允许查看 addr/link/route/neigh/rule,拒绝对象或动作:%q", object)
	}
	if index >= len(args) {
		return nil
	}
	action := strings.ToLower(strings.TrimSpace(args[index]))
	if _, ok := actions[action]; !ok {
		if _, dangerous := dangerousIPSubcommands[action]; dangerous {
			return fmt.Errorf("ip 命令只允许查看,拒绝修改、执行或持续监听子命令:%q", args[index])
		}
		return fmt.Errorf("ip 对象 %q 仅允许 show/list/get 查看动作,拒绝:%q", object, args[index])
	}
	return nil
}

func validateReadOnlyDateArgs(args []string) error {
	expectsValue := false
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		lower := strings.ToLower(trimmed)
		if expectsValue {
			expectsValue = false
			continue
		}
		if lower == "-s" || strings.HasPrefix(lower, "-s") && len(lower) > 2 ||
			lower == "--set" || strings.HasPrefix(lower, "--set=") {
			return fmt.Errorf("date 仅允许查看或格式化时间,拒绝修改系统时间的参数:%q", arg)
		}
		if strings.HasPrefix(trimmed, "+") {
			continue
		}
		switch trimmed {
		case "-u", "--utc", "--universal", "-R", "--rfc-email", "--debug", "--help", "--version", "--resolution":
			continue
		case "-d", "--date", "-f", "--file", "-r", "--reference":
			expectsValue = true
			continue
		}
		if strings.HasPrefix(lower, "--date=") || strings.HasPrefix(lower, "--file=") ||
			strings.HasPrefix(lower, "--reference=") || strings.HasPrefix(lower, "--iso-8601") ||
			strings.HasPrefix(lower, "--rfc-3339=") {
			continue
		}
		return fmt.Errorf("date 仅允许查看或格式化时间,拒绝可能修改系统时间的参数:%q", arg)
	}
	if expectsValue {
		return fmt.Errorf("date 查看参数缺少取值")
	}
	return nil
}
