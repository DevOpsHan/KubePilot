package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// weComChannel 企业微信群机器人。企微 markdown 只有 content 字段(无标题)。
type weComChannel struct {
	webhook string
	client  *http.Client
}

func (c *weComChannel) Name() string { return "wecom" }

func (c *weComChannel) Send(ctx context.Context, msg Message) error {
	body, _ := json.Marshal(map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"content": msg.Text,
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhook, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("企业微信返回 HTTP %d", resp.StatusCode)
	}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if r.ErrCode != 0 {
		return fmt.Errorf("企业微信业务错误 %d: %s", r.ErrCode, r.ErrMsg)
	}
	return nil
}
