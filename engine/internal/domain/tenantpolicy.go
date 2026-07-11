package domain

import (
	"fmt"
	"regexp"
)

// Well-known tenant-policy keys. A policy is a protected per-tenant key/value
// entry written only through the plugin seam — tenants cannot change it via
// the HTTP API. Consumers read by key; unknown keys are simply absent.
const (
	PolicySystemPromptPrefix      = "system_prompt_prefix"
	PolicyWidgetAttribution       = "widget_attribution"
	PolicyActiveUsersLimit        = "active_users_limit"
	PolicyActiveUsersMode         = "active_users_mode"
	PolicyKnowledgeDocumentsLimit = "knowledge_documents_limit"
	PolicySchemasLimit            = "schemas_limit"
)

// policyKeyPattern is the allowed shape of a policy key; it mirrors the DB
// CHECK constraint so callers get a typed error before a round-trip.
var policyKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,100}$`)

// maxPolicyValueLength caps a policy value so an oversized write is rejected
// before it reaches the database.
const maxPolicyValueLength = 8192

// TenantPolicy is one protected per-tenant key/value entry. Key selects the
// policy; Value is its opaque string payload (consumers parse it as needed).
type TenantPolicy struct {
	Key   string
	Value string
}

// Validate checks that a TenantPolicy is well-formed. The DB CHECK constraint
// on the key is a backstop; this is the primary gate so callers get a typed
// error before a round-trip.
func (p TenantPolicy) Validate() error {
	if !policyKeyPattern.MatchString(p.Key) {
		return fmt.Errorf("key must match %s, got %q", policyKeyPattern.String(), p.Key)
	}
	if len(p.Value) > maxPolicyValueLength {
		return fmt.Errorf("value must be at most %d bytes, got %d", maxPolicyValueLength, len(p.Value))
	}
	return nil
}
