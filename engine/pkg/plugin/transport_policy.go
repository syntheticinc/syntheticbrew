package plugin

import "fmt"

// TransportPolicy decides which MCP transport types are permitted for new
// MCP server registrations in the current deployment. Managed / multi-tenant
// environments reject stdio-based transports (local command execution);
// CE / self-hosted environments accept all.
type TransportPolicy interface {
	// IsAllowed returns nil if the transport is permitted; otherwise an
	// ErrTransportNotAllowed error that the delivery layer maps to 400.
	IsAllowed(transportType string) error
}

// PermissiveTransportPolicy accepts every known transport. Used by CE / bare-metal.
type PermissiveTransportPolicy struct{}

// IsAllowed always returns nil — all transports are permitted in CE mode.
func (PermissiveTransportPolicy) IsAllowed(string) error { return nil }

// RestrictedTransportPolicy blocks stdio/shell transports that execute
// arbitrary binaries on the engine host. Used in hosted / multi-tenant deployments.
type RestrictedTransportPolicy struct{}

// IsAllowed returns ErrTransportNotAllowed for stdio (the only transport type
// that executes local commands). All network-bound types (http, sse,
// streamable-http) are accepted.
func (RestrictedTransportPolicy) IsAllowed(transportType string) error {
	if transportType == "stdio" {
		return ErrTransportNotAllowed(transportType)
	}
	return nil
}

// ErrTransportNotAllowed is the typed sentinel the delivery layer matches on.
type ErrTransportNotAllowed string

func (e ErrTransportNotAllowed) Error() string {
	return fmt.Sprintf("MCP transport %q is not permitted in this deployment", string(e))
}
