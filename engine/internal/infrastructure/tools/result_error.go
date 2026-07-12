package tools

import "strings"

// ErrorResultPrefix marks a tool result string as an application-level failure.
// The engine's tool convention returns application failures as normal
// (string, nil) results carrying this prefix — a non-nil Go error is reserved
// for transport/platform faults (network down, MCP server crashed) so the ReAct
// loop does not abort on a recoverable outcome.
//
// The marker is the single signal every result consumer keys off: the ReAct
// loop-breaker, the LLM content classifier, and the MCP server endpoint, which
// maps a marked result to the JSON-RPC tools/call isError flag.
const ErrorResultPrefix = "[ERROR]"

// IsErrorResult reports whether a tool result string signals a failure via the
// [ERROR] convention. It is intentionally strict about position (prefix, not
// substring) so a success payload that merely mentions the marker in its body
// is never misclassified.
func IsErrorResult(result string) bool {
	return strings.HasPrefix(result, ErrorResultPrefix)
}

// SanitizeDBError maps a raw database error to a short, constant, client-safe
// message. It never echoes internal identifiers — constraint names, table
// names, column names, or SQLSTATE codes — so a tool result surfaced to an MCP
// client (or the LLM) cannot leak the engine's Postgres schema.
//
// Detection is by substring on the driver's error text: pgx/GORM embed both the
// SQLSTATE code and the "... constraint" phrase, so a dependency-light string
// match classifies the common violations without a pgconn type assertion (which
// also lets sqlite-phrased errors like "UNIQUE constraint failed" map through).
// Anything unrecognised collapses to a generic message rather than the raw text.
func SanitizeDBError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "23505"),
		strings.Contains(msg, "duplicate key"),
		strings.Contains(msg, "unique constraint"):
		return "already exists"
	case strings.Contains(msg, "23514"),
		strings.Contains(msg, "check constraint"):
		return "one or more fields have an invalid value"
	case strings.Contains(msg, "23503"),
		strings.Contains(msg, "foreign key constraint"):
		return "still referenced by other records"
	case strings.Contains(msg, "23502"),
		strings.Contains(msg, "not-null constraint"),
		strings.Contains(msg, "not null constraint"):
		return "a required field is missing"
	default:
		return "the operation could not be completed"
	}
}
