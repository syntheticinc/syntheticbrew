//go:build integration

package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syntheticinc/syntheticbrew/internal/app"
	"github.com/syntheticinc/syntheticbrew/pkg/config"
)

// TestModelBoot_LoadsDefaultFromDB is the regression guard for the partner's
// "chat 502 after any non-deploy restart" bug. It exercises the real boot
// path (NewInfraComponents) against the real testcontainer Postgres with the
// Liquibase schema and the suite-seeded is_default chat model.
//
// Scenario: the default chat model exists ONLY in the DB (set via API/brewctl)
// and NO LLM env/config default is present — exactly what a brewctl-configured
// deployment looks like after an eviction/drain/OOM/crash restart.
//
// RED→GREEN: against the pre-fix boot path (default built only from cfg.LLM),
// an empty cfg.LLM yields a nil chat model so AgentService is skipped and chat
// returns 502. With the DB-authoritative boot fix, the engine loads the DB
// default and AgentService initializes.
func TestModelBoot_LoadsDefaultFromDB(t *testing.T) {
	requireSuite(t)

	// Empty LLM config = the bug scenario (no env default; model lives in DB).
	cfg := config.DefaultConfig()
	cfg.LLM.DefaultProvider = ""

	comps, err := app.NewInfraComponents(app.InfraComponentsConfig{
		Config: *cfg,
		DB:     testDB, // testcontainer DB; setupSuite seeded an is_default chat model
	})
	require.NoError(t, err)
	require.NotNil(t, comps)

	require.NotNil(t, comps.AgentService,
		"AgentService must initialize from the DB default chat model so chat survives a non-deploy restart (no 502)")
	assert.Equal(t, "test-chat-model", comps.ModelName,
		"boot must load the suite-seeded DB default model name")
}
