package http

import (
	"errors"
	"regexp"

	"github.com/google/uuid"
)

// nameRegex enforces a DNS-label-like format for operator-facing resource
// identifiers (schemas, knowledge bases). Matches k8s namespace conventions
// so URL params are always router-safe.
var nameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// reservedNames are tokens that would shadow or collide with route segments
// on `/api/v1/{schemas,knowledge-bases}/{name}/...`. Forbidden at validation
// to keep route resolution unambiguous regardless of chi matching order.
var reservedNames = map[string]struct{}{
	"chat":            {},
	"agents":          {},
	"agent-relations": {},
	"memory":          {},
	"files":           {},
	"health":          {},
	"auth":            {},
	"tasks":           {},
	"models":          {},
	"knowledge-bases": {},
	"schemas":         {},
	"mcp-servers":     {},
	"tokens":          {},
	"sessions":        {},
	"metrics":         {},
}

// Validation sentinels — handlers map them to 400 with a human-readable cause.
var (
	ErrInvalidName    = errors.New("invalid resource name")
	ErrUUIDShapedName = errors.New("resource name must not be UUID-shaped")
	ErrReservedName   = errors.New("resource name is reserved (collides with route segment)")
	ErrNameTooLong    = errors.New("resource name exceeds 100 characters")
	ErrNameEmpty      = errors.New("resource name is empty")
)

// MaxResourceNameLength is the upper bound on schema/KB names. Matches the
// Liquibase CHECK constraint applied in DB so HTTP and storage agree.
const MaxResourceNameLength = 100

// ValidateResourceName enforces the DNS-label format, length cap, reserved
// list, and UUID-shape rejection on operator-supplied schema/KB names.
//
// Called at:
//   - URL handler param resolution (every renamed `{name}` route)
//   - Request body on POST/PUT (CreateSchema, CreateKnowledgeBase)
//
// Not called on PATCH bodies — name field on PATCH is rejected upstream
// with 409 Conflict (immutability).
func ValidateResourceName(name string) error {
	if len(name) == 0 {
		return ErrNameEmpty
	}
	if len(name) > MaxResourceNameLength {
		return ErrNameTooLong
	}
	if !nameRegex.MatchString(name) {
		return ErrInvalidName
	}
	if _, err := uuid.Parse(name); err == nil {
		return ErrUUIDShapedName
	}
	if _, reserved := reservedNames[name]; reserved {
		return ErrReservedName
	}
	return nil
}
