// Package server exposes the engine server entry point as a public API.
//
// External wrappers import this package, build a Config (optionally setting
// a Plugin via Config.Plugin), and call Run. The default Noop plugin is
// used when none is provided.
package server

import "github.com/syntheticinc/syntheticbrew/internal/app"

// Config is the engine server configuration.
type Config = app.ServerConfig

// Run starts the engine server with the given configuration and blocks until
// the server exits.
func Run(sc Config) error {
	return app.Run(sc)
}
