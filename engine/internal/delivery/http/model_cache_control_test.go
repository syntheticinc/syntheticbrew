package http

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateCacheControl(t *testing.T) {
	cases := []struct {
		name    string
		cc      *CacheControlPayload
		wantErr bool
	}{
		{"nil is off", nil, false},
		{"disabled empty", &CacheControlPayload{Enabled: false}, false},
		{"valid full", &CacheControlPayload{Enabled: true, Breakpoints: []string{"system", "tools", "history"}, MinPrefixTokens: 2048}, false},
		{"valid no breakpoints", &CacheControlPayload{Enabled: true}, false},
		{"bad breakpoint", &CacheControlPayload{Enabled: true, Breakpoints: []string{"system", "bogus"}}, true},
		{"negative min prefix", &CacheControlPayload{Enabled: true, MinPrefixTokens: -1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateCacheControl(tc.cc)
			if tc.wantErr {
				assert.NotEmpty(t, msg, "malformed config must yield a 400-grade message")
			} else {
				assert.Empty(t, msg)
			}
		})
	}
}
