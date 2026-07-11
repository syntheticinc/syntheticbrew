// Package mcp re-exports the TransportPolicy types from pkg/plugin so that
// internal packages can import them via the shorter mcpcatalog alias without
// depending on the public plugin package directly. External modules
// must import pkg/plugin instead to satisfy Go's internal package visibility rules.
package mcp

import "github.com/syntheticinc/syntheticbrew/pkg/plugin"

// TransportPolicy is an alias for plugin.TransportPolicy.
type TransportPolicy = plugin.TransportPolicy

// PermissiveTransportPolicy is an alias for plugin.PermissiveTransportPolicy.
type PermissiveTransportPolicy = plugin.PermissiveTransportPolicy

// RestrictedTransportPolicy is an alias for plugin.RestrictedTransportPolicy.
type RestrictedTransportPolicy = plugin.RestrictedTransportPolicy

// ErrTransportNotAllowed is an alias for plugin.ErrTransportNotAllowed.
type ErrTransportNotAllowed = plugin.ErrTransportNotAllowed
