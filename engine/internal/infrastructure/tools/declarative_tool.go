package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
	"github.com/syntheticinc/syntheticbrew/pkg/secrets"
)

// NewDeclarativeTool creates an HTTP-based tool from YAML configuration.
// It makes HTTP requests based on the endpoint, method, headers, and auth settings
// defined in CustomToolConfig.
func NewDeclarativeTool(cfg config.CustomToolConfig) tool.InvokableTool {
	return NewDeclarativeToolWithClient(cfg, &http.Client{})
}

// NewDeclarativeToolWithClient creates a DeclarativeTool with a custom HTTP client (for testing).
func NewDeclarativeToolWithClient(cfg config.CustomToolConfig, client *http.Client) tool.InvokableTool {
	return &declarativeTool{cfg: cfg, client: client}
}

type declarativeTool struct {
	cfg    config.CustomToolConfig
	client *http.Client
}

func (t *declarativeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	params := make(map[string]*schema.ParameterInfo, len(t.cfg.Params))
	for _, p := range t.cfg.Params {
		params[p.Name] = &schema.ParameterInfo{
			Type:     schema.DataType(p.Type),
			Desc:     p.Description,
			Required: p.Required,
		}
	}

	return &schema.ToolInfo{
		Name:        t.cfg.Name,
		Desc:        t.cfg.Description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}, nil
}

func (t *declarativeTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	method, baseURL := parseEndpoint(t.cfg.Endpoint, t.cfg.Method)

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	reqURL, body, err := buildRequest(method, baseURL, args)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	t.applyHeaders(req, body != nil)
	t.applyAuth(req)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody)), nil
	}

	return string(respBody), nil
}

func (t *declarativeTool) applyHeaders(req *http.Request, hasBody bool) {
	for k, v := range t.cfg.Headers {
		req.Header.Set(k, v)
	}
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
}

func (t *declarativeTool) applyAuth(req *http.Request) {
	if t.cfg.Auth == nil || t.cfg.Auth.Type != "bearer" {
		return
	}

	token := secrets.Lookup(t.cfg.Auth.TokenEnv)
	if token == "" {
		return
	}

	req.Header.Set("Authorization", "Bearer "+token)
}

// buildRequest constructs URL and body based on HTTP method.
// GET/HEAD: params go as query string. Others: params go as JSON body.
func buildRequest(method, baseURL string, args map[string]interface{}) (string, io.Reader, error) {
	if method == "GET" || method == "HEAD" {
		u, err := url.Parse(baseURL)
		if err != nil {
			return "", nil, fmt.Errorf("parse URL: %w", err)
		}
		q := u.Query()
		for k, v := range args {
			q.Set(k, fmt.Sprintf("%v", v))
		}
		u.RawQuery = q.Encode()
		return u.String(), nil, nil
	}

	data, err := json.Marshal(args)
	if err != nil {
		return "", nil, fmt.Errorf("marshal body: %w", err)
	}

	return baseURL, strings.NewReader(string(data)), nil
}

// parseEndpoint extracts HTTP method and URL from an endpoint string.
// Supports "METHOD URL" format (e.g., "GET https://api.example.com/data")
// or falls back to separate method field.
func parseEndpoint(endpoint, method string) (string, string) {
	parts := strings.SplitN(endpoint, " ", 2)
	if len(parts) == 2 {
		m := strings.ToUpper(parts[0])
		if isHTTPMethod(m) {
			return m, parts[1]
		}
	}

	if method == "" {
		method = "GET"
	}

	return strings.ToUpper(method), endpoint
}

func isHTTPMethod(m string) bool {
	switch m {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD":
		return true
	}
	return false
}
