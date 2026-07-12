package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// dingTalkChannel 钉钉群自定义机器人。配置 secret 时按钉钉规范加签。
type dingTalkChannel struct {
	webhook string
	secret  string
	client  *http.Client
}

func (c *dingTalkChannel) Name() string { return "dingtalk" }

func (c *dingTalkChannel) Send(ctx context.Context, msg Message) error {
	body, _ := json.Marshal(map[string]any{
		"msgtype": "markdown",
		"markdown": map[string]string{
			"title": msg.Title,
			"text":  msg.Text,
		},
	})
	endpoint := c.signedURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		return fmt.Errorf("钉钉返回 HTTP %d", resp.StatusCode)
	}
	var r struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	if r.ErrCode != 0 {
		return fmt.Errorf("钉钉业务错误 %d: %s", r.ErrCode, r.ErrMsg)
	}
	return nil
}

// signedURL 若配置了 secret,则拼接 timestamp + HMAC-SHA256 加签参数。
func (c *dingTalkChannel) signedURL() string {
	if c.secret == "" {
		return c.webhook
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(c.secret))
	mac.Write([]byte(ts + "\n" + c.secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	sep := "?"
	if strings.Contains(c.webhook, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%stimestamp=%s&sign=%s", c.webhook, sep, ts, url.QueryEscape(sign))
}
