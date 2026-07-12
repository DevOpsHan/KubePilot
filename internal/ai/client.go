// Package ai 封装对大模型的调用(OpenAI 兼容接口,支持工具调用与流式输出)。
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client 是 OpenAI 兼容的聊天补全客户端(适用于 DeepSeek/通义/Kimi 等)。
type Client struct {
	apiKey    string
	baseURL   string
	model     string
	maxTokens int
	temp      float32
	http      *http.Client
}

// NewClient 创建客户端。baseURL 形如 https://api.deepseek.com。
func NewClient(baseURL, apiKey, model string, maxTokens, timeoutSec int, temp float32) *Client {
	return &Client{
		apiKey:    apiKey,
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     model,
		maxTokens: maxTokens,
		temp:      temp,
		http:      &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// ModelName 返回当前客户端使用的模型名称,用于运行时状态展示与测试。
func (c *Client) ModelName() string {
	if c == nil {
		return ""
	}
	return c.model
}

// Message 一条对话消息(兼容 OpenAI 格式,含工具调用)。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	// ReasoningContent 推理模型(如 DeepSeek 思考模式/通义 thinking)的思维链。
	// 关键:多轮工具调用时,带 tool_calls 的 assistant 消息必须把 reasoning_content 原样回传,
	// 否则思考模式接口返回 400 "The reasoning_content in the thinking mode must be passed back to the API"。
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

// ToolCall 模型发起的一次工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 工具调用的函数名与参数(JSON 字符串)。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool 提供给模型的工具定义。
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef 工具的函数描述与参数 schema。
type FunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type completionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	Temperature float32   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Stream      bool      `json:"stream"`
}

type completionResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete 非流式对话(可带工具),返回完整回复消息。用于 AI 诊断等场景。
func (c *Client) Complete(ctx context.Context, messages []Message, tools []Tool) (*Message, error) {
	reqBody := completionRequest{
		Model: c.model, Messages: messages, Tools: tools,
		Temperature: c.temp, MaxTokens: c.maxTokens, Stream: false,
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用模型失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var out completionResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析模型响应失败: %w (原始: %s)", err, truncate(string(body), 200))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("模型返回错误: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("模型无返回内容(HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}
	return &out.Choices[0].Message, nil
}

// streamChunk 流式响应的单个数据块。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

// CompleteStream 流式对话:逐 token 通过 onToken 回调推出,返回累积后的完整消息(含工具调用)。
func (c *Client) CompleteStream(ctx context.Context, messages []Message, tools []Tool, onToken func(string)) (*Message, error) {
	reqBody := completionRequest{
		Model: c.model, Messages: messages, Tools: tools,
		Temperature: c.temp, MaxTokens: c.maxTokens, Stream: true,
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用模型失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("模型返回错误(HTTP %d): %s", resp.StatusCode, truncate(string(body), 200))
	}

	var contentBuf strings.Builder
	var reasoningBuf strings.Builder
	toolCalls := map[int]*ToolCall{}
	maxIdx := -1

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 放大缓冲,防止长行截断
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		// 思维链只累积、不作为答案 token 推给前端(前端"思考中"占位已够);但必须留存以便多轮回传
		if delta.ReasoningContent != "" {
			reasoningBuf.WriteString(delta.ReasoningContent)
		}
		if delta.Content != "" {
			contentBuf.WriteString(delta.Content)
			if onToken != nil {
				onToken(delta.Content)
			}
		}
		// 工具调用按 index 增量累积
		for _, tcd := range delta.ToolCalls {
			tc, ok := toolCalls[tcd.Index]
			if !ok {
				tc = &ToolCall{Type: "function"}
				toolCalls[tcd.Index] = tc
				if tcd.Index > maxIdx {
					maxIdx = tcd.Index
				}
			}
			if tcd.ID != "" {
				tc.ID = tcd.ID
			}
			if tcd.Function.Name != "" {
				tc.Function.Name = tcd.Function.Name
			}
			tc.Function.Arguments += tcd.Function.Arguments
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取模型流失败: %w", err)
	}

	msg := &Message{Role: "assistant", Content: contentBuf.String(), ReasoningContent: reasoningBuf.String()}
	for i := 0; i <= maxIdx; i++ {
		if tc, ok := toolCalls[i]; ok {
			msg.ToolCalls = append(msg.ToolCalls, *tc)
		}
	}
	return msg, nil
}

// Chat 非流式简单对话(无工具),返回回复文本。用于 AI 诊断等场景。
func (c *Client) Chat(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	msg, err := c.Complete(ctx, []Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(msg.Content), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
