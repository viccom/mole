package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type UserInfo struct {
	OpenID    string `json:"open_id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type Client struct {
	appID      string
	appSecret  string
	httpClient *http.Client

	mu          sync.RWMutex
	tenantToken string
	tokenExpire time.Time
}

func NewClient(appID, appSecret string) *Client {
	return &Client{
		appID:      appID,
		appSecret:  appSecret,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c.appID != "" && c.appSecret != ""
}

func (c *Client) AppID() string {
	return c.appID
}

func (c *Client) GetTenantAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.tenantToken != "" && time.Now().Before(c.tokenExpire) {
		token := c.tenantToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tenantToken != "" && time.Now().Before(c.tokenExpire) {
		return c.tenantToken, nil
	}

	token, err := c.getTenantTokenFromURL(ctx, "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal")
	if err != nil {
		return "", err
	}
	return token, nil
}

func (c *Client) getTenantTokenFromURL(ctx context.Context, url string) (string, error) {
	body := map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	}
	respBody, err := c.doRequest(ctx, "POST", url, body, "")
	if err != nil {
		return "", fmt.Errorf("get tenant access token: %w", err)
	}

	var resp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("parse tenant token response: %w", err)
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("feishu API error: %d %s", resp.Code, resp.Msg)
	}

	expire := resp.Expire - 60
	if expire <= 0 {
		expire = 60
	}
	c.tenantToken = resp.TenantAccessToken
	c.tokenExpire = time.Now().Add(time.Duration(expire) * time.Second)

	slog.Debug("Feishu tenant access token refreshed", "expire", resp.Expire)
	return c.tenantToken, nil
}

func (c *Client) GetUserAccessToken(ctx context.Context, code string) (string, error) {
	tenantToken, err := c.GetTenantAccessToken(ctx)
	if err != nil {
		return "", err
	}
	return c.getUserAccessTokenFromURL(ctx, code, tenantToken, "https://open.feishu.cn/open-apis/authen/v1/oidc/access_token")
}

func (c *Client) getUserAccessTokenFromURL(ctx context.Context, code, tenantToken, url string) (string, error) {
	body := map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	}
	respBody, err := c.doRequest(ctx, "POST", url, body, tenantToken)
	if err != nil {
		return "", fmt.Errorf("get user access token: %w", err)
	}

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("parse user token response: %w", err)
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("feishu API error: %d %s", resp.Code, resp.Msg)
	}

	return resp.Data.AccessToken, nil
}

func (c *Client) GetUserInfo(ctx context.Context, userAccessToken string) (*UserInfo, error) {
	return c.getUserInfoFromURL(ctx, userAccessToken, "https://open.feishu.cn/open-apis/authen/v1/user_info")
}

func (c *Client) getUserInfoFromURL(ctx context.Context, userAccessToken, url string) (*UserInfo, error) {
	respBody, err := c.doRequest(ctx, "GET", url, nil, userAccessToken)
	if err != nil {
		return nil, fmt.Errorf("get user info: %w", err)
	}

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			OpenID    string `json:"open_id"`
			Name      string `json:"name"`
			AvatarURL string `json:"avatar_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse user info response: %w", err)
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("feishu API error: %d %s", resp.Code, resp.Msg)
	}

	return &UserInfo{
		OpenID:    resp.Data.OpenID,
		Name:      resp.Data.Name,
		AvatarURL: resp.Data.AvatarURL,
	}, nil
}

func (c *Client) GetUserByCode(ctx context.Context, code string) (*UserInfo, error) {
	userAccessToken, err := c.GetUserAccessToken(ctx, code)
	if err != nil {
		return nil, err
	}
	return c.GetUserInfo(ctx, userAccessToken)
}

func (c *Client) doRequest(ctx context.Context, method, url string, body any, accessToken string) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feishu API HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
