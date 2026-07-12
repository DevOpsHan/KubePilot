// Package prom 提供 Prometheus HTTP API 的轻量查询客户端(不引入官方重依赖)。
// 当前用于巡检引擎的指标类规则(节点 CPU/内存阈值)。
package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client Prometheus 查询客户端。
type Client struct {
	baseURL string
	http    *http.Client
}

// New 创建客户端。baseURL 形如 http://prometheus:9090。
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// queryResponse Prometheus /api/v1/query 响应结构(仅取所需字段)。
type queryResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"` // [unix_ts, "数值字符串"]
		} `json:"result"`
	} `json:"data"`
}

// Sample 一条即时查询结果样本:完整标签集 + 数值。
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Query 执行即时查询,返回带完整标签的样本列表(vector 结果)。供自定义指标规则按任意标签取对象名。
func (c *Client) Query(ctx context.Context, promql string) ([]Sample, error) {
	endpoint := c.baseURL + "/api/v1/query?query=" + url.QueryEscape(promql)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus 返回 HTTP %d", resp.StatusCode)
	}
	var body queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("解析 Prometheus 响应失败: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("Prometheus 查询失败: %s", body.Error)
	}
	if body.Data.ResultType != "vector" {
		return nil, fmt.Errorf("非 vector 结果: %s", body.Data.ResultType)
	}
	samples := make([]Sample, 0, len(body.Data.Result))
	for _, item := range body.Data.Result {
		if len(item.Value) != 2 {
			continue
		}
		raw, ok := item.Value[1].(string)
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		samples = append(samples, Sample{Labels: item.Metric, Value: v})
	}
	return samples, nil
}

// QueryVector 执行即时查询,返回 instance(缺省取 node)标签 → 数值,供内置指标规则使用。
func (c *Client) QueryVector(ctx context.Context, promql string) (map[string]float64, error) {
	samples, err := c.Query(ctx, promql)
	if err != nil {
		return nil, err
	}
	values := make(map[string]float64, len(samples))
	for _, s := range samples {
		key := s.Labels["instance"]
		if key == "" {
			key = s.Labels["node"]
		}
		if key == "" {
			continue
		}
		values[key] = s.Value
	}
	return values, nil
}

// Ping 检查 Prometheus 可达性(用一次轻量查询,兼容未暴露 /-/ready 的托管服务)。
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.QueryVector(ctx, "vector(1)")
	// vector(1) 无 instance 标签,返回空 map 也算成功;仅传输/协议错误视为不可达
	return err
}
