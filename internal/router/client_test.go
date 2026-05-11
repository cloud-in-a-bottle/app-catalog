package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// installerCall records what the test handler saw on a request.
type installerCall struct {
	Method        string
	Path          string
	Body          string
	Authorization string
}

// newInstallerServer accepts any request under
// /api/services/v2/call/installer/.  Tests inspect the call to verify
// the catalog hit the right sub-endpoint.
func newInstallerServer(
	t *testing.T,
	handler func(call installerCall, w http.ResponseWriter),
) (*httptest.Server, *[]installerCall) {
	t.Helper()
	calls := []installerCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/services/v2/call/installer/") {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		call := installerCall{
			Method:        r.Method,
			Path:          r.URL.Path,
			Body:          string(bodyBytes),
			Authorization: r.Header.Get("Authorization"),
		}
		calls = append(calls, call)
		handler(call, w)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestDeploySendsCorrectURLAndBody(t *testing.T) {
	var received installerCall
	srv, calls := newInstallerServer(t, func(c installerCall, w http.ResponseWriter) {
		received = c
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":       true,
			"app_name": "newapp",
			"status":   "building",
		})
	})

	cli := NewClient(srv.URL, 5*time.Second)
	result, err := cli.Deploy(context.Background(), "test-token", "https://github.com/foo/bar", "newapp")
	if err != nil {
		t.Fatalf("Deploy returned error: %v", err)
	}
	if result.AppName != "newapp" || result.Status != "building" {
		t.Fatalf("unexpected result: %+v", result)
	}

	if len(*calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(*calls))
	}
	if received.Method != "POST" {
		t.Errorf("method: got %q want POST", received.Method)
	}
	if received.Path != "/api/services/v2/call/installer/install" {
		t.Errorf("path: got %q", received.Path)
	}
	if received.Authorization != "Bearer test-token" {
		t.Errorf("auth: got %q", received.Authorization)
	}
	if !strings.Contains(received.Body, `"repo_url":"https://github.com/foo/bar"`) {
		t.Errorf("body missing repo_url: %s", received.Body)
	}
	if !strings.Contains(received.Body, `"app_name":"newapp"`) {
		t.Errorf("body missing app_name: %s", received.Body)
	}
}

func TestDeployEmptyTokenFails(t *testing.T) {
	cli := NewClient("http://invalid", 5*time.Second)
	_, err := cli.Deploy(context.Background(), "", "https://github.com/foo/bar", "x")
	if err == nil {
		t.Fatal("expected error on empty token, got nil")
	}
	if !strings.Contains(err.Error(), "OPENHOST_APP_TOKEN") {
		t.Errorf("error mentions wrong env var: %v", err)
	}
}

func TestDeployPermissionRequiredSurfacesGrantURL(t *testing.T) {
	srv, _ := newInstallerServer(t, func(c installerCall, w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":   "permission_required",
			"message": "no installer grant matches",
			"required_grant": map[string]any{
				"scope":     "global",
				"grant":     map[string]any{"capability": "install", "repo_url_prefix": "https://github.com/foo/bar"},
				"grant_url": "https://zone.example/permissions/approve?app=catalog&service=installer",
			},
		})
	})

	cli := NewClient(srv.URL, 5*time.Second)
	_, err := cli.Deploy(context.Background(), "tok", "https://github.com/foo/bar", "x")
	if err == nil {
		t.Fatal("expected PermissionRequiredError, got nil")
	}

	perm, ok := err.(*PermissionRequiredError)
	if !ok {
		t.Fatalf("expected *PermissionRequiredError, got %T: %v", err, err)
	}
	if !strings.Contains(perm.GrantURL, "permissions/approve") {
		t.Errorf("missing grant URL: %q", perm.GrantURL)
	}
}

func TestDeployServerErrorBubbles(t *testing.T) {
	srv, _ := newInstallerServer(t, func(c installerCall, w http.ResponseWriter) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"install_failed","message":"manifest invalid"}`))
	})

	cli := NewClient(srv.URL, 5*time.Second)
	_, err := cli.Deploy(context.Background(), "tok", "https://github.com/foo/bar", "x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if _, ok := err.(*PermissionRequiredError); ok {
		t.Fatalf("non-403 error must not be classified as permission_required: %v", err)
	}
	if !strings.Contains(err.Error(), "manifest invalid") {
		t.Errorf("error message lost: %v", err)
	}
}

func TestAppStatusUsesStatusEndpoint(t *testing.T) {
	var received installerCall
	srv, _ := newInstallerServer(t, func(c installerCall, w http.ResponseWriter) {
		received = c
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "running",
			"error":  nil,
		})
	})

	cli := NewClient(srv.URL, 5*time.Second)
	result, err := cli.AppStatus(context.Background(), "tok", "myapp")
	if err != nil {
		t.Fatalf("AppStatus error: %v", err)
	}
	if result.Status != "running" {
		t.Errorf("unexpected status: %+v", result)
	}
	if received.Method != "GET" {
		t.Errorf("expected GET, got %q", received.Method)
	}
	if received.Path != "/api/services/v2/call/installer/status/myapp" {
		t.Errorf("path: got %q", received.Path)
	}
}

func TestAppLogsUsesLogsEndpoint(t *testing.T) {
	var received installerCall
	srv, _ := newInstallerServer(t, func(c installerCall, w http.ResponseWriter) {
		received = c
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("log line 1\nlog line 2\n"))
	})

	cli := NewClient(srv.URL, 5*time.Second)
	logs, err := cli.AppLogs(context.Background(), "tok", "myapp")
	if err != nil {
		t.Fatalf("AppLogs error: %v", err)
	}
	if !strings.Contains(logs, "log line 1") {
		t.Errorf("logs missing content: %q", logs)
	}
	if received.Path != "/api/services/v2/call/installer/logs/myapp" {
		t.Errorf("path: got %q", received.Path)
	}
}
