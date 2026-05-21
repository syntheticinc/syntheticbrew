// SyntheticBrew Engine — Community Edition entry point.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/syntheticinc/syntheticbrew/internal/app"
)

var (
	version = "dev-ce"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	showVersion := flag.Bool("version", false, "Print version and exit")
	port := flag.Int("port", 0, "Override server port (0 = use config)")
	managed := flag.Bool("managed", false, "Run in managed subprocess mode")
	flag.Parse()

	if *showVersion {
		fmt.Printf("syntheticbrew-ce %s (commit: %s, built: %s)\n", version, commit, date)
		os.Exit(0)
	}

	configExplicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			configExplicit = true
		}
	})

	sc := app.ServerConfig{
		ConfigPath:     *configPath,
		ConfigExplicit: configExplicit,
		Port:           *port,
		Managed:        *managed,
		RequireTenant:  false,
		Version:        version,
		Commit:         commit,
		Date:           date,
	}

	if err := app.Run(sc); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
