package collector

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// LogStreamOptions 控制 Pod 日志读取行为。
type LogStreamOptions struct {
	Container  string // 目标容器;空表示默认(单容器 Pod 可留空)
	Follow     bool   // 是否持续跟随(tail -f 语义)
	TailLines  *int64 // 尾部行数;nil 表示不限制
	Timestamps bool   // 是否在每行前附加时间戳
	Previous   bool   // 是否读取上一次终止容器的日志
}

// ListPodContainers 返回指定 Pod 的容器名列表(含 init 容器),供前端选择目标容器。
func (c *Collector) ListPodContainers(ctx context.Context, namespace, pod string) ([]string, error) {
	if c == nil || c.clientset == nil {
		return nil, fmt.Errorf("K8S 客户端未就绪")
	}
	reqCtx, cancel := context.WithTimeout(ctx, defaultKubernetesRequestTimeout)
	defer cancel()
	p, err := c.clientset.CoreV1().Pods(namespace).Get(reqCtx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(p.Spec.InitContainers)+len(p.Spec.Containers))
	for i := range p.Spec.InitContainers {
		names = append(names, p.Spec.InitContainers[i].Name)
	}
	for i := range p.Spec.Containers {
		names = append(names, p.Spec.Containers[i].Name)
	}
	return names, nil
}

// StreamPodLogs 打开 Pod 日志流,调用方负责关闭返回的 ReadCloser。
// follow=true 时该流会持续输出,直到 ctx 取消或对端结束。
func (c *Collector) StreamPodLogs(ctx context.Context, namespace, pod string, opts LogStreamOptions) (io.ReadCloser, error) {
	if c == nil || c.clientset == nil {
		return nil, fmt.Errorf("K8S 客户端未就绪")
	}
	logOpts := &corev1.PodLogOptions{
		Container:  opts.Container,
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
		Previous:   opts.Previous,
		TailLines:  opts.TailLines,
	}
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(pod, logOpts)
	return req.Stream(ctx)
}

// ExecOptions 定义在 Pod 容器内执行命令的参数与流。
type ExecOptions struct {
	Container string    // 目标容器;空表示默认容器
	Command   []string  // 待执行命令 argv(直接传递,不经 shell 拼接,无注入风险)
	TTY       bool      // 是否分配伪终端
	Stdin     io.Reader // 标准输入流;nil 表示不接入 stdin
	Stdout    io.Writer // 标准输出流
	Stderr    io.Writer // 标准错误流(TTY 模式下被忽略)
	Resize    remotecommand.TerminalSizeQueue
}

// ExecInPod 在指定 Pod 容器内执行命令,通过 WebSocket 与 API Server 建立双向流。
// 依赖 restConfig(经带 config 的构造函数注入);fake client 场景下为 nil,直接报错。
func (c *Collector) ExecInPod(ctx context.Context, namespace, pod string, opts ExecOptions) error {
	if c == nil || c.clientset == nil {
		return fmt.Errorf("K8S 客户端未就绪")
	}
	if c.restConfig == nil {
		return fmt.Errorf("当前集群运行时缺少 REST 配置,无法建立 exec 连接")
	}
	if len(opts.Command) == 0 {
		return fmt.Errorf("exec 命令不能为空")
	}

	execReq := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: opts.Container,
			Command:   opts.Command,
			Stdin:     opts.Stdin != nil,
			Stdout:    opts.Stdout != nil,
			Stderr:    opts.Stderr != nil && !opts.TTY,
			TTY:       opts.TTY,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewWebSocketExecutor(c.restConfig, "GET", execReq.URL().String())
	if err != nil {
		return fmt.Errorf("创建 exec 执行器失败: %w", err)
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             opts.Stdin,
		Stdout:            opts.Stdout,
		Stderr:            opts.Stderr,
		Tty:               opts.TTY,
		TerminalSizeQueue: opts.Resize,
	})
}
