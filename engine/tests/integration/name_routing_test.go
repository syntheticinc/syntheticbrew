//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TC-NAME-01: POST /schemas with invalid name → 400 with cause.
//
// Engine 1.1.0+ enforces DNS-label-style names at the HTTP layer
// (regex `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max 100 chars). Confirms the
// validation chain reaches the boundary, not just the unit tests.
func TestNAME01_CreateSchema_InvalidName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	cases := []struct {
		label string
		name  string
	}{
		{"slash", "bad/name"},
		{"uppercase", "BadName"},
		{"underscore", "bad_name"},
		{"leading-hyphen", "-bad"},
		{"trailing-hyphen", "bad-"},
		{"uuid-shaped", "550e8400-e29b-41d4-a716-446655440000"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			resp := do(t, http.MethodPost, "/api/v1/schemas",
				mustJSON(map[string]any{"name": tc.name}), adminToken)
			body := readBody(t, resp)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", body)
		})
	}
}

// TC-NAME-02: POST /schemas with reserved name → 400.
//
// Reserved tokens (chat, agents, agent-relations, memory, files, …) are
// rejected at validation to prevent URL-segment collision with route
// patterns. Defense-in-depth: even if the audit middleware uses chi route
// patterns, blocking creation keeps the operator-facing surface clean.
func TestNAME02_CreateSchema_ReservedName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	reserved := []string{"chat", "agents", "agent-relations", "memory", "files", "schemas"}
	for _, name := range reserved {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPost, "/api/v1/schemas",
				mustJSON(map[string]any{"name": name}), adminToken)
			body := readBody(t, resp)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", body)
			assert.Contains(t, string(body), "reserved", "body=%s", body)
		})
	}
}

// TC-NAME-03: POST /knowledge-bases with invalid name → 400.
//
// Same regex applies to KBs. Confirms KB CREATE handler wires the validator
// in addition to the URL-param validation on subsequent reads.
func TestNAME03_CreateKB_InvalidName(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	resp := do(t, http.MethodPost, "/api/v1/knowledge-bases",
		mustJSON(map[string]any{"name": "Bad/KB-Name"}), adminToken)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", body)
}

// TC-NAME-04: GET on URL with invalid name format → 400 (validation),
// not 404. Distinguishes "valid format, missing row" (404) from "wouldn't
// match a real row anyway" (400).
func TestNAME04_GetSchema_InvalidNameFormat(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet, "/api/v1/schemas/Bad%2FName", nil, adminToken)
	body := readBody(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "body=%s", body)
}
