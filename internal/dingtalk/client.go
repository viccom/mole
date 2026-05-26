package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type UserInfo struct {
	UnionID   string `json:"union_id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

type Client struct {
	appKey     string
	appSecret  string
	corpID     string
	httpClient *http.Client

	mu           sync.RWMutex
	accessToken  string
	tokenExpire  time.Time
}

func NewClient(appKey, appSecret, corpID string) *Client {
	return &Client{
		appKey:     appKey,
		appSecret:  appSecret,
		corpID:     corpID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) Enabled() bool {
	return c.appKey != "" && c.appSecret != ""
}

func (c *Client) CorpID() string {
	return c.corpID
}

func (c *Client) AppKey() string {
	return c.appKey
}

func (c *Client) getAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpire) {
		token := c.accessToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpire) {
		return c.accessToken, nil
	}

	u, _ := url.Parse("https://oapi.dingtalk.com/gettoken")
	q := u.Query()
	q.Set("appkey", c.appKey)
	q.Set("appsecret", c.appSecret)
	u.RawQuery = q.Encode()
	respBody, err := c.doRequest(ctx, "GET", u.String(), nil, "")
	if err != nil {
		return "", fmt.Errorf("get dingtalk access token: %w", err)
	}

	var resp struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("parse access token response: %w", err)
	}
	if resp.ErrCode != 0 {
		return "", fmt.Errorf("dingtalk API error: %d %s", resp.ErrCode, resp.ErrMsg)
	}

	expire := resp.ExpiresIn - 60
	if expire <= 0 {
		expire = 60
	}
	c.accessToken = resp.AccessToken
	c.tokenExpire = time.Now().Add(time.Duration(expire) * time.Second)

	slog.Debug("DingTalk access token refreshed", "expires_in", resp.ExpiresIn)
	return c.accessToken, nil
}

// GetUserByCode handles H5 JSAPI login: code → userid → user info
func (c *Client) GetUserByCode(ctx context.Context, code string) (*UserInfo, error) {
	accessToken, err := c.getAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	// code → userid
	body := map[string]string{"code": code}
	respBody, err := c.doRequest(ctx, "POST",
		"https://oapi.dingtalk.com/topapi/v2/user/getuserinfo?access_token="+accessToken,
		body, "")
	if err != nil {
		return nil, fmt.Errorf("get dingtalk userid by code: %w", err)
	}

	var userResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Result  struct {
			UserID string `json:"userid"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &userResp); err != nil {
		return nil, fmt.Errorf("parse userid response: %w", err)
	}
	if userResp.ErrCode != 0 {
		return nil, fmt.Errorf("dingtalk getuserid error: %d %s", userResp.ErrCode, userResp.ErrMsg)
	}
	if userResp.Result.UserID == "" {
		return nil, fmt.Errorf("dingtalk returned empty userid")
	}

	// userid → user details
	return c.getUserByUserID(ctx, accessToken, userResp.Result.UserID)
}

// GetUserByOAuthCode handles PC OAuth2 login: authCode → userAccessToken → user info
func (c *Client) GetUserByOAuthCode(ctx context.Context, authCode string) (*UserInfo, error) {
	// authCode → userAccessToken
	body := map[string]string{
		"clientId":     c.appKey,
		"clientSecret": c.appSecret,
		"code":         authCode,
		"grantType":    "authorization_code",
	}
	respBody, err := c.doRequest(ctx, "POST",
		"https://api.dingtalk.com/v1.0/oauth2/userAccessToken", body, "")
	if err != nil {
		return nil, fmt.Errorf("get dingtalk user access token: %w", err)
	}

	var tokenResp struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse user access token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("dingtalk returned empty user access token")
	}

	// userAccessToken → user info
	return c.getUserInfoByUserAccessToken(ctx, tokenResp.AccessToken)
}

func (c *Client) getUserByUserID(ctx context.Context, accessToken, userID string) (*UserInfo, error) {
	body := map[string]string{"userid": userID}
	respBody, err := c.doRequest(ctx, "POST",
		"https://oapi.dingtalk.com/topapi/v2/user/get?access_token="+accessToken,
		body, "")
	if err != nil {
		return nil, fmt.Errorf("get dingtalk user detail: %w", err)
	}

	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Result  struct {
			Name    string `json:"name"`
			Avatar  string `json:"avatar"`
			UnionID string `json:"unionid"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse user detail response: %w", err)
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("dingtalk get user error: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	if resp.Result.UnionID == "" {
		return nil, fmt.Errorf("dingtalk returned empty unionid")
	}

	return &UserInfo{
		UnionID:   resp.Result.UnionID,
		Name:      resp.Result.Name,
		AvatarURL: resp.Result.Avatar,
	}, nil
}

func (c *Client) getUserInfoByUserAccessToken(ctx context.Context, userAccessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.dingtalk.com/v1.0/contact/users/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-acs-oauth2-user-access-token", userAccessToken)

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
		return nil, fmt.Errorf("dingtalk get user info HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var info struct {
		Nick      string `json:"nick"`
		AvatarURL string `json:"avatarUrl"`
		OpenID    string `json:"openId"`
		UnionID   string `json:"unionId"`
	}
	if err := json.Unmarshal(respBody, &info); err != nil {
		return nil, fmt.Errorf("parse dingtalk user info: %w", err)
	}
	if info.UnionID == "" {
		return nil, fmt.Errorf("dingtalk returned empty unionId")
	}

	return &UserInfo{
		UnionID:   info.UnionID,
		Name:      info.Nick,
		AvatarURL: info.AvatarURL,
	}, nil
}

func (c *Client) doRequest(ctx context.Context, method, rawURL string, body any, accessToken string) ([]byte, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

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
		return nil, fmt.Errorf("dingtalk API HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
