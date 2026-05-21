package mcp_test

import (
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/service/mcp"
)

func TestPermissiveTransportPolicy(t *testing.T) {
	p := mcp.PermissiveTransportPolicy{}

	t.Run("accepts stdio", func(t *testing.T) {
		if err := p.IsAllowed("stdio"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("accepts http", func(t *testing.T) {
		if err := p.IsAllowed("http"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("accepts sse", func(t *testing.T) {
		if err := p.IsAllowed("sse"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("accepts streamable-http", func(t *testing.T) {
		if err := p.IsAllowed("streamable-http"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
}

func TestRestrictedTransportPolicy(t *testing.T) {
	p := mcp.RestrictedTransportPolicy{}

	t.Run("rejects stdio", func(t *testing.T) {
		err := p.IsAllowed("stdio")
		if err == nil {
			t.Fatal("expected error for stdio, got nil")
		}
		var notAllowed mcp.ErrTransportNotAllowed
		if _, ok := err.(mcp.ErrTransportNotAllowed); !ok {
			t.Errorf("expected ErrTransportNotAllowed, got %T: %v", err, err)
		}
		_ = notAllowed
	})

	t.Run("accepts http", func(t *testing.T) {
		if err := p.IsAllowed("http"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("accepts sse", func(t *testing.T) {
		if err := p.IsAllowed("sse"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})

	t.Run("accepts streamable-http", func(t *testing.T) {
		if err := p.IsAllowed("streamable-http"); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
}
