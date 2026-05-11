// Package router talks to the OpenHost router on behalf of the
// catalog.  All cross-app calls go through the v2 shortname-based
// service proxy at /api/services/v2/call/<shortname>/<rest>, where
// <shortname> is the name the catalog declared in its manifest's
// [[services.v2.consumes]] block.  The catalog declares shortname
// "installer" pointing at the router's built-in installer service.
package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// InstallerShortname must match the ``shortname`` field of the
// catalog's [[services.v2.consumes]] manifest entry.  Used as a path
// segment in /api/services/v2/call/<shortname>/<rest>.
const InstallerShortname = "installer"

func installerPath(rest string) string {
	return "/api/services/v2/call/" + InstallerShortname + "/" + strings.TrimLeft(rest, "/")
}

type Client struct {
	baseURL string
	http    *http.Client
}

type DeployResult struct {
	AppName string
	Status  string
}

type AppStatusResult struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

type installResponse struct {
	OK      bool   `json:"ok"`
	AppName string `json:"app_name"`
	Status  string `json:"status"`
}

// PermissionRequiredError is returned when the router replies 403 with
// a required_grant body, indicating the owner has not yet granted the
// catalog the installer permission for this repo_url.
type PermissionRequiredError struct {
	Message  string
	GrantURL string
}

func (e *PermissionRequiredError) Error() string {
	if strings.TrimSpace(e.GrantURL) != "" {
		return fmt.Sprintf("%s (grant at %s)", e.Message, e.GrantURL)
	}
	return e.Message
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Deploy installs ``repoURL`` as ``appName`` via the installer v2
// service.  ``token`` is the catalog's own OPENHOST_APP_TOKEN.
func (c *Client) Deploy(
	ctx context.Context,
	token string,
	repoURL string,
	appName string,
) (DeployResult, error) {
	if strings.TrimSpace(token) == "" {
		return DeployResult{}, errors.New("OPENHOST_APP_TOKEN is empty")
	}

	body, err := json.Marshal(map[string]string{
		"repo_url": repoURL,
		"app_name": appName,
	})
	if err != nil {
		return DeployResult{}, fmt.Errorf("marshal install request: %w", err)
	}

	resp, err := c.callInstaller(ctx, token, http.MethodPost, "install", body)
	if err != nil {
		return DeployResult{}, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))

	if resp.StatusCode == http.StatusForbidden {
		return DeployResult{}, parsePermissionRequired(respBody)
	}
	if resp.StatusCode >= 400 {
		return DeployResult{}, fmt.Errorf("installer returned %s: %s", resp.Status, errorMessage(respBody))
	}

	var out installResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return DeployResult{}, fmt.Errorf("decode install response: %w", err)
	}
	if !out.OK {
		return DeployResult{}, fmt.Errorf("installer rejected install: %s", errorMessage(respBody))
	}
	if out.AppName == "" {
		out.AppName = appName
	}
	if out.Status == "" {
		out.Status = "building"
	}
	return DeployResult{AppName: out.AppName, Status: out.Status}, nil
}

// AppStatus queries the installer's /status/<appName> sub-endpoint.
// The router only returns status for apps the calling consumer (the
// catalog) actually installed.
func (c *Client) AppStatus(ctx context.Context, token string, appName string) (AppStatusResult, error) {
	resp, err := c.callInstaller(ctx, token, http.MethodGet, "status/"+appName, nil)
	if err != nil {
		return AppStatusResult{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 400 {
		return AppStatusResult{}, fmt.Errorf("status returned %s: %s", resp.Status, errorMessage(body))
	}
	var out AppStatusResult
	if err := json.Unmarshal(body, &out); err != nil {
		return AppStatusResult{}, fmt.Errorf("decode status response: %w", err)
	}
	return out, nil
}

// AppLogs queries the installer's /logs/<appName> sub-endpoint.  Same
// caller-scoped visibility as AppStatus.
func (c *Client) AppLogs(ctx context.Context, token string, appName string) (string, error) {
	resp, err := c.callInstaller(ctx, token, http.MethodGet, "logs/"+appName, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("logs returned %s: %s", resp.Status, errorMessage(body))
	}
	return string(body), nil
}

// callInstaller dispatches a v2 shortname-call to the installer
// sub-endpoint.  ``body`` may be nil for GETs.
func (c *Client) callInstaller(
	ctx context.Context,
	token string,
	method string,
	endpoint string,
	body []byte,
) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+installerPath(endpoint), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create installer request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call installer service: %w", err)
	}
	return resp, nil
}

// parsePermissionRequired extracts a PermissionRequiredError from a 403
// body of the post-PR67 v2 shape (``{required_grant: {grant, scope,
// grant_url}}``).  Falls back to a plain error if the body is malformed.
func parsePermissionRequired(body []byte) error {
	var b map[string]any
	if json.Unmarshal(body, &b) != nil {
		return fmt.Errorf("permission denied: %s", errorMessage(body))
	}
	grant, _ := b["required_grant"].(map[string]any)
	if grant == nil {
		return fmt.Errorf("permission denied: %s", errorMessage(body))
	}
	grantURL, _ := grant["grant_url"].(string)
	msg, _ := b["message"].(string)
	if msg == "" {
		msg = "permission required"
	}
	return &PermissionRequiredError{Message: msg, GrantURL: grantURL}
}

// errorMessage extracts the most informative message from a router
// error response body, falling back to the raw body or a placeholder.
func errorMessage(body []byte) string {
	var b map[string]any
	if json.Unmarshal(body, &b) == nil {
		for _, key := range []string{"message", "error"} {
			if v, ok := b[key].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
	}
	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		return trimmed
	}
	return "unknown error"
}
