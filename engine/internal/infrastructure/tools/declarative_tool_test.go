package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

func TestDeclarativeTool_Info(t *testing.T) {
	cfg := config.CustomToolConfig{
		Name:        "get_weather",
		Description: "Get current weather",
		Endpoint:    "GET https://api.weather.com/current",
		Params: []config.CustomToolParam{
			{Name: "city", Type: "string", Description: "City name", Required: true},
		},
	}

	dt := NewDeclarativeTool(cfg)
	info, err := dt.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "get_weather", info.Name)
	assert.Equal(t, "Get current weather", info.Desc)
}

func TestDeclarativeTool_GET_QueryParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "London", r.URL.Query().Get("city"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"temp":20}`))
	}))
	defer server.Close()

	cfg := config.CustomToolConfig{
		Name:     "get_weather",
		Endpoint: "GET " + server.URL + "/weather",
		Params: []config.CustomToolParam{
			{Name: "city", Type: "string", Required: true},
		},
	}

	dt := NewDeclarativeToolWithClient(cfg, server.Client())
	result, err := dt.InvokableRun(context.Background(), `{"city":"London"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"temp":20}`, result)
}

func TestDeclarativeTool_POST_JSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "Buy milk", body["title"])

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"order-1"}`))
	}))
	defer server.Close()

	cfg := config.CustomToolConfig{
		Name:     "create_order",
		Endpoint: "POST " + server.URL + "/orders",
		Params: []config.CustomToolParam{
			{Name: "title", Type: "string", Required: true},
		},
	}

	dt := NewDeclarativeToolWithClient(cfg, server.Client())
	result, err := dt.InvokableRun(context.Background(), `{"title":"Buy milk"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"order-1"}`, result)
}

func TestDeclarativeTool_CustomHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "my-app", r.Header.Get("X-App-Name"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	cfg := config.CustomToolConfig{
		Name:     "test_tool",
		Endpoint: "GET " + server.URL,
		Headers:  map[string]string{"X-App-Name": "my-app"},
	}

	dt := NewDeclarativeToolWithClient(cfg, server.Client())
	result, err := dt.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Equal(t, "ok", result)
}

func TestDeclarativeTool_BearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token-123", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("authorized"))
	}))
	defer server.Close()

	t.Setenv("TEST_API_TOKEN", "test-token-123")

	cfg := config.CustomToolConfig{
		Name:     "auth_tool",
		Endpoint: "GET " + server.URL,
		Auth:     &config.ToolAuthConfig{Type: "bearer", TokenEnv: "TEST_API_TOKEN"},
	}

	dt := NewDeclarativeToolWithClient(cfg, server.Client())
	result, err := dt.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Equal(t, "authorized", result)
}

func TestDeclarativeTool_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	cfg := config.CustomToolConfig{
		Name:     "fail_tool",
		Endpoint: "GET " + server.URL,
	}

	dt := NewDeclarativeToolWithClient(cfg, server.Client())
	result, err := dt.InvokableRun(context.Background(), `{}`)
	require.NoError(t, err)
	assert.Contains(t, result, "HTTP 404")
	assert.Contains(t, result, "not found")
}

func TestDeclarativeTool_InvalidJSON(t *testing.T) {
	cfg := config.CustomToolConfig{
		Name:     "test",
		Endpoint: "GET https://example.com",
	}

	dt := NewDeclarativeTool(cfg)
	_, err := dt.InvokableRun(context.Background(), `{bad json`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse args")
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		method     string
		wantMethod string
		wantURL    string
	}{
		{
			name:       "GET in endpoint",
			endpoint:   "GET https://api.example.com/data",
			method:     "",
			wantMethod: "GET",
			wantURL:    "https://api.example.com/data",
		},
		{
			name:       "POST in endpoint",
			endpoint:   "POST https://api.example.com/orders",
			method:     "",
			wantMethod: "POST",
			wantURL:    "https://api.example.com/orders",
		},
		{
			name:       "DELETE in endpoint",
			endpoint:   "DELETE https://api.example.com/items/1",
			method:     "",
			wantMethod: "DELETE",
			wantURL:    "https://api.example.com/items/1",
		},
		{
			name:       "separate method field",
			endpoint:   "https://api.example.com/data",
			method:     "PUT",
			wantMethod: "PUT",
			wantURL:    "https://api.example.com/data",
		},
		{
			name:       "default to GET",
			endpoint:   "https://api.example.com/data",
			method:     "",
			wantMethod: "GET",
			wantURL:    "https://api.example.com/data",
		},
		{
			name:       "lowercase method in endpoint is normalized to uppercase",
			endpoint:   "get https://api.example.com/data",
			method:     "",
			wantMethod: "GET",
			wantURL:    "https://api.example.com/data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method, url := parseEndpoint(tt.endpoint, tt.method)
			assert.Equal(t, tt.wantMethod, method)
			assert.Equal(t, tt.wantURL, url)
		})
	}
}

func TestDeclarativeTool_MethodFromSeparateField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("updated"))
	}))
	defer server.Close()

	cfg := config.CustomToolConfig{
		Name:     "update_tool",
		Endpoint: server.URL + "/items/1",
		Method:   "PUT",
	}

	dt := NewDeclarativeToolWithClient(cfg, server.Client())
	result, err := dt.InvokableRun(context.Background(), `{"name":"new name"}`)
	require.NoError(t, err)
	assert.Equal(t, "updated", result)
}
