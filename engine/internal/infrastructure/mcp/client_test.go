package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTransport struct {
	startErr  error
	responses map[string]*Response
	sendErr   error
	notified  []*Request
	closed    bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		responses: make(map[string]*Response),
	}
}

func (m *mockTransport) Start(_ context.Context) error {
	return m.startErr
}

func (m *mockTransport) Send(_ context.Context, req *Request) (*Response, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	resp, ok := m.responses[req.Method]
	if !ok {
		return nil, fmt.Errorf("unexpected method: %s", req.Method)
	}
	return resp, nil
}

func (m *mockTransport) Notify(_ context.Context, req *Request) {
	m.notified = append(m.notified, req)
}

func (m *mockTransport) Close() error {
	m.closed = true
	return nil
}

func makeToolsResponse(tools []MCPTool) *Response {
	result, _ := json.Marshal(ToolsListResult{Tools: tools})
	return &Response{JSONRPC: "2.0", ID: 2, Result: result}
}

func makeInitResponse() *Response {
	result, _ := json.Marshal(map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
	})
	return &Response{JSONRPC: "2.0", ID: 1, Result: result}
}

func TestClient_Connect(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*mockTransport)
		wantErr   bool
		wantTools int
	}{
		{
			name: "successful connect with tools",
			setup: func(m *mockTransport) {
				m.responses["initialize"] = makeInitResponse()
				m.responses["tools/list"] = makeToolsResponse([]MCPTool{
					{Name: "read_file", Description: "Read a file"},
					{Name: "write_file", Description: "Write a file"},
				})
			},
			wantTools: 2,
		},
		{
			name: "successful connect with no tools",
			setup: func(m *mockTransport) {
				m.responses["initialize"] = makeInitResponse()
				m.responses["tools/list"] = makeToolsResponse(nil)
			},
			wantTools: 0,
		},
		{
			name: "transport start error",
			setup: func(m *mockTransport) {
				m.startErr = fmt.Errorf("connection refused")
			},
			wantErr: true,
		},
		{
			name: "initialize error",
			setup: func(m *mockTransport) {
				m.sendErr = fmt.Errorf("initialize failed")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newMockTransport()
			tt.setup(transport)

			client := NewClient("test-server", transport)
			err := client.Connect(context.Background())

			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, client.IsConnected())
				return
			}
			require.NoError(t, err)
			assert.True(t, client.IsConnected())
			assert.Len(t, client.ListTools(), tt.wantTools)
		})
	}
}

func TestClient_ConnectSendsInitializedNotification(t *testing.T) {
	transport := newMockTransport()
	transport.responses["initialize"] = makeInitResponse()
	transport.responses["tools/list"] = makeToolsResponse(nil)

	client := NewClient("test", transport)
	err := client.Connect(context.Background())
	require.NoError(t, err)

	require.Len(t, transport.notified, 1)
	assert.Equal(t, "notifications/initialized", transport.notified[0].Method)
}

func TestClient_CallTool(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*mockTransport)
		wantErr  bool
		wantText string
	}{
		{
			name: "successful call",
			setup: func(m *mockTransport) {
				result, _ := json.Marshal(ToolCallResult{
					Content: []ToolContent{{Type: "text", Text: "file contents here"}},
				})
				m.responses["tools/call"] = &Response{JSONRPC: "2.0", ID: 3, Result: result}
			},
			wantText: "file contents here",
		},
		{
			name: "rpc error",
			setup: func(m *mockTransport) {
				m.responses["tools/call"] = &Response{
					JSONRPC: "2.0",
					ID:      3,
					Error:   &RPCError{Code: -32600, Message: "tool not found"},
				}
			},
			wantErr: true,
		},
		{
			name: "transport error",
			setup: func(m *mockTransport) {
				m.sendErr = fmt.Errorf("connection lost")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newMockTransport()
			tt.setup(transport)

			client := NewClient("test", transport)
			result, _, err := client.CallTool(context.Background(), "read_file", map[string]interface{}{"path": "/tmp/x"})

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantText, result)
		})
	}
}

func TestClient_ListToolsBeforeConnect(t *testing.T) {
	client := NewClient("test", newMockTransport())
	assert.Empty(t, client.ListTools())
	assert.False(t, client.IsConnected())
}

func TestClient_Close(t *testing.T) {
	transport := newMockTransport()
	transport.responses["initialize"] = makeInitResponse()
	transport.responses["tools/list"] = makeToolsResponse(nil)

	client := NewClient("test", transport)
	require.NoError(t, client.Connect(context.Background()))
	assert.True(t, client.IsConnected())

	require.NoError(t, client.Close())
	assert.False(t, client.IsConnected())
	assert.True(t, transport.closed)
}

func TestClient_CallTool_IsError(t *testing.T) {
	tests := []struct {
		name        string
		result      json.RawMessage
		wantText    string
		wantIsError bool
	}{
		{
			name:        "isError true returns content and flag",
			result:      json.RawMessage(`{"content":[{"type":"text","text":"ERROR: service unavailable"}],"isError":true}`),
			wantText:    "ERROR: service unavailable",
			wantIsError: true,
		},
		{
			name:        "isError false returns content without flag",
			result:      json.RawMessage(`{"content":[{"type":"text","text":"all good"}],"isError":false}`),
			wantText:    "all good",
			wantIsError: false,
		},
		{
			name:        "isError omitted defaults to false",
			result:      json.RawMessage(`{"content":[{"type":"text","text":"result"}]}`),
			wantText:    "result",
			wantIsError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport := newMockTransport()
			transport.responses["tools/call"] = &Response{JSONRPC: "2.0", ID: 3, Result: tt.result}

			client := NewClient("test", transport)
			result, isError, err := client.CallTool(context.Background(), "my_tool", nil)

			require.NoError(t, err)
			assert.Equal(t, tt.wantText, result)
			assert.Equal(t, tt.wantIsError, isError)
		})
	}
}

func TestClient_Name(t *testing.T) {
	client := NewClient("my-server", newMockTransport())
	assert.Equal(t, "my-server", client.Name())
}
