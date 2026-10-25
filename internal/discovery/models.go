// Package discovery reads a provider's model catalogue without invoking models
// or executing configuration expressions.
package discovery

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Fetch follows Crush's generic discovery contract: base URL + /models,
// Bearer authentication, and a JSON data array. Redirects are rejected so a
// server cannot forward credentials to another endpoint.
func Fetch(ctx context.Context, baseURL, apiKey string) ([]Model, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("模型发现需要有效的 HTTP(S) API 地址")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/models"
	u.RawPath = ""
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("无法创建模型列表请求（地址详情已隐藏）")
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("模型发现已取消或超时")
		}
		return nil, errors.New("模型列表请求失败，请检查地址、网络和凭据（详情已隐藏）")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("模型列表返回 HTTP %d", resp.StatusCode)
	}
	const maxResponse = 8 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, errors.New("读取模型列表失败")
	}
	if len(body) > maxResponse {
		return nil, errors.New("模型列表超过 8 MiB 限制")
	}
	var result struct {
		Data    []Model `json:"data"`
		HasMore bool    `json:"has_more"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Data == nil {
		return nil, errors.New("模型列表格式无效，需要包含 data 数组的 JSON（响应详情已隐藏）")
	}
	if result.HasMore {
		return nil, errors.New("服务返回了分页模型列表，当前仅支持完整的 /models data 列表；未导入不完整结果")
	}
	return result.Data, nil
}
