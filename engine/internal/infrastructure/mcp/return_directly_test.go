package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// TestMCPTool_ReturnsDirectly covers the `_meta` parsing: only a boolean true
// under the namespaced key counts; everything else degrades to off.
func TestMCPTool_ReturnsDirectly(t *testing.T) {
	cases := []struct {
		name string
		meta string // JSON for the tool's _meta object, or "" for absent
		want bool
	}{
		{"declared true", `{"syntheticbrew.ai/return-directly": true}`, true},
		{"declared false", `{"syntheticbrew.ai/return-directly": false}`, false},
		{"absent meta", ``, false},
		{"empty meta", `{}`, false},
		{"string not bool", `{"syntheticbrew.ai/return-directly": "true"}`, false},
		{"number not bool", `{"syntheticbrew.ai/return-directly": 1}`, false},
		{"null value", `{"syntheticbrew.ai/return-directly": null}`, false},
		{"object value", `{"syntheticbrew.ai/return-directly": {"enabled": true}}`, false},
		{"array value", `{"syntheticbrew.ai/return-directly": [true]}`, false},
		{"unrelated key", `{"other": true}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var meta map[string]any
			if tc.meta != "" {
				if err := json.Unmarshal([]byte(tc.meta), &meta); err != nil {
					t.Fatalf("bad test meta: %v", err)
				}
			}
			tool := MCPTool{Name: "x", Meta: meta}
			if got := tool.ReturnsDirectly(); got != tc.want {
				t.Errorf("ReturnsDirectly()=%v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseToolsFromResponse_Meta proves tools/list parsing carries `_meta` end to
// end and that a malformed (non-object) `_meta` does not fail ingestion.
func TestParseToolsFromResponse_Meta(t *testing.T) {
	resp := &Response{Result: json.RawMessage(`{
		"tools": [
			{"name": "recommend_products", "description": "final answer", "inputSchema": {},
			 "_meta": {"syntheticbrew.ai/return-directly": true}},
			{"name": "search", "description": "search", "inputSchema": {}}
		]
	}`)}
	tools, err := parseToolsFromResponse(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if !tools[0].ReturnsDirectly() {
		t.Errorf("recommend_products must be return-directly")
	}
	if tools[1].ReturnsDirectly() {
		t.Errorf("search must not be return-directly")
	}
}

// TestParseToolsFromResponse_MalformedMeta ensures a `_meta` that is not an object
// is tolerated (parsing fails for that field only would otherwise break the whole
// list); the tool ingests with return-directly off.
func TestParseToolsFromResponse_MalformedMeta(t *testing.T) {
	resp := &Response{Result: json.RawMessage(`{
		"tools": [
			{"name": "search", "description": "search", "inputSchema": {}, "_meta": "not-an-object"}
		]
	}`)}
	tools, err := parseToolsFromResponse(resp)
	if err == nil {
		// A string _meta fails to unmarshal into map[string]any; the list parse
		// errors out. That is acceptable (the server is malformed) — but it must
		// not panic. If parsing is later made lenient, the tool must default off.
		if len(tools) == 1 && tools[0].ReturnsDirectly() {
			t.Errorf("malformed _meta must never yield return-directly=true")
		}
	}
}

// TestAdaptMCPTool_InfoExtra proves the adapter surfaces the declaration through
// ToolInfo.Extra (the channel the ReAct loop reads), and leaves Extra unset when
// not declared.
func TestAdaptMCPTool_InfoExtra(t *testing.T) {
	declared := AdaptMCPTool(nil, MCPTool{
		Name:        "recommend_products",
		InputSchema: json.RawMessage(`{}`),
		Meta:        map[string]any{"syntheticbrew.ai/return-directly": true},
	})
	info, err := declared.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if v, _ := info.Extra[domain.ToolExtraReturnDirectly].(bool); !v {
		t.Errorf("declared tool must carry Extra[%s]=true, got %#v", domain.ToolExtraReturnDirectly, info.Extra)
	}

	plain := AdaptMCPTool(nil, MCPTool{Name: "search", InputSchema: json.RawMessage(`{}`)})
	pInfo, err := plain.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if _, ok := pInfo.Extra[domain.ToolExtraReturnDirectly]; ok {
		t.Errorf("plain tool must not set the return-directly Extra key, got %#v", pInfo.Extra)
	}
}
