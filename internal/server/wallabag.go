package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// wallabagClient owns its credentials and tokens; wallabago's global token
// cannot be reset after a rejected refresh and is not concurrency safe.
type wallabagClient struct {
	cfg        Config
	http       *http.Client
	mu         sync.Mutex // protects token acquisition, rotation, and failure cooldown
	token      oauthToken
	expires    time.Time
	retryAfter time.Time
	authErr    error
}

type oauthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func newWallabagClient(cfg Config) *wallabagClient {
	cfg.WallabagURL = strings.TrimRight(cfg.WallabagURL, "/")
	return &wallabagClient{cfg: cfg, http: &http.Client{
		Timeout: 30 * time.Second,
		// Never forward credentials or replay a mutation across redirects.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (c *wallabagClient) auth(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.token.AccessToken != "" && time.Now().Before(c.expires) {
		return c.token.AccessToken, nil
	}
	if time.Now().Before(c.retryAfter) {
		return "", c.authErr
	}
	grant := "password"
	if c.token.RefreshToken != "" {
		grant = "refresh_token"
	}
	token, status, err := c.fetchToken(ctx, grant)
	if err != nil && grant == "refresh_token" && (status == 400 || status == 401) {
		// The API request has not been sent, so reauthentication is safe for all
		// methods. Forget the rejected refresh even if the password grant fails.
		c.token = oauthToken{}
		token, _, err = c.fetchToken(ctx, "password")
	}
	if err != nil {
		// Bound authentication traffic from concurrent callers during an outage.
		// A canceled caller must not poison the next caller's authentication.
		if ctx.Err() == nil {
			c.authErr = err
			c.retryAfter = time.Now().Add(time.Second)
		}
		return "", err
	}
	if grant == "refresh_token" && token.RefreshToken == "" {
		token.RefreshToken = c.token.RefreshToken
	}
	c.token = token
	c.expires = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	c.authErr = nil
	c.retryAfter = time.Time{}
	return token.AccessToken, nil
}

func (c *wallabagClient) fetchToken(ctx context.Context, grant string) (oauthToken, int, error) {
	var token oauthToken
	form := url.Values{"grant_type": {grant}, "client_id": {c.cfg.WallabagClientID}, "client_secret": {c.cfg.WallabagClientSecret}}
	if grant == "refresh_token" {
		form.Set("refresh_token", c.token.RefreshToken)
	} else {
		form.Set("username", c.cfg.WallabagUsername)
		form.Set("password", c.cfg.WallabagPassword)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WallabagURL+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return token, 0, errors.New("wallabag: invalid token endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	// Do not expose OAuth bodies, credentials, or transport errors containing URLs.
	if err != nil {
		return token, 0, errors.New("wallabag: token request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return token, resp.StatusCode, fmt.Errorf("wallabag: %s grant returned HTTP %d", grant, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&token); err != nil || token.AccessToken == "" || token.ExpiresIn <= 0 || token.ExpiresIn > int64((1<<63-1)/time.Second) {
		return oauthToken{}, resp.StatusCode, errors.New("wallabag: invalid token response")
	}
	return token, resp.StatusCode, nil
}

// apiCall authenticates before dispatch and never retries an API request, even
// on 401 or transport failure: a mutation may already have taken effect.
func (c *wallabagClient) apiCall(ctx context.Context, path, method string, data []byte) ([]byte, error) {
	token, err := c.auth(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.WallabagURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("wallabag: invalid API request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("wallabag: API request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("wallabag: API returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("wallabag: reading API response failed")
	}
	return body, nil
}

func (c *wallabagClient) getJSON(ctx context.Context, path string, dst any) error {
	body, err := c.apiCall(ctx, path, http.MethodGet, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return errors.New("wallabag: invalid API response")
	}
	return nil
}
