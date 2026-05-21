package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// stubCatalogRepo is an in-memory repo implementation for service-level tests.
// The real implementation (configrepo.GORMMCPCatalogRepository) is covered by
// its own GORM-backed integration tests.
type stubCatalogRepo struct {
	records []domain.MCPCatalogRecord
}

func (s *stubCatalogRepo) List(_ context.Context) ([]domain.MCPCatalogRecord, error) {
	out := make([]domain.MCPCatalogRecord, len(s.records))
	copy(out, s.records)
	return out, nil
}

func (s *stubCatalogRepo) GetByName(_ context.Context, name string) (*domain.MCPCatalogRecord, error) {
	for i := range s.records {
		if s.records[i].Name == name {
			r := s.records[i]
			return &r, nil
		}
	}
	return nil, nil
}

func fixtureRepo() *stubCatalogRepo {
	return &stubCatalogRepo{
		records: []domain.MCPCatalogRecord{
			{
				Name:        "tavily-web-search",
				Display:     "Tavily Web Search",
				Description: "AI-optimized web search",
				Category:    domain.MCPCategorySearch,
				Verified:    true,
				Packages: []domain.MCPCatalogPackage{{
					Type:    "stdio",
					Command: "npx",
					Args:    []string{"-y", "@mcptools/mcp-tavily"},
					EnvVars: []domain.MCPCatalogEnvVar{{
						Name:        "TAVILY_API_KEY",
						Description: "Get key at tavily.com",
						Required:    true,
						Secret:      true,
					}},
				}},
				ProvidedTools: []domain.MCPCatalogTool{{Name: "tavily_search", Description: "Search the web"}},
			},
			{
				Name:        "brave-search",
				Display:     "Brave Search",
				Description: "Privacy-focused web search",
				Category:    domain.MCPCategorySearch,
				Verified:    true,
			},
			{
				Name:        "github",
				Display:     "GitHub",
				Description: "Create issues, PRs, search code",
				Category:    domain.MCPCategoryDevTools,
				Verified:    true,
			},
			{
				Name:        "slack",
				Display:     "Slack",
				Description: "Send messages, read channels",
				Category:    domain.MCPCategoryCommunication,
				Verified:    true,
			},
		},
	}
}

func TestCatalogService_List(t *testing.T) {
	svc := NewCatalogService(fixtureRepo())

	entries := svc.List()
	assert.Len(t, entries, 4)
	assert.Equal(t, CatalogVersion, svc.Version())
}

func TestCatalogService_ListByCategory(t *testing.T) {
	svc := NewCatalogService(fixtureRepo())

	search := svc.ListByCategory(domain.MCPCategorySearch)
	assert.Len(t, search, 2)

	devTools := svc.ListByCategory(domain.MCPCategoryDevTools)
	assert.Len(t, devTools, 1)
	assert.Equal(t, "github", devTools[0].Name)

	payments := svc.ListByCategory(domain.MCPCategoryPayments)
	assert.Len(t, payments, 0)
}

func TestCatalogService_Search(t *testing.T) {
	svc := NewCatalogService(fixtureRepo())

	results := svc.Search("search")
	assert.Len(t, results, 3) // tavily + brave + github (all have "search" in name/desc)

	results = svc.Search("github")
	assert.Len(t, results, 1)
	assert.Equal(t, "github", results[0].Name)

	results = svc.Search("nonexistent")
	assert.Len(t, results, 0)
}

func TestCatalogService_GetByName(t *testing.T) {
	svc := NewCatalogService(fixtureRepo())

	entry, ok := svc.GetByName(context.Background(), "tavily-web-search")
	require.True(t, ok)
	assert.Equal(t, "Tavily Web Search", entry.Display)
	assert.True(t, entry.Verified)
	assert.Equal(t, domain.MCPCategorySearch, entry.Category)
	require.Len(t, entry.Packages, 1)
	assert.Equal(t, "stdio", entry.Packages[0].Type)
	require.Len(t, entry.ProvidedTools, 1)
	assert.Equal(t, "tavily_search", entry.ProvidedTools[0].Name)

	_, ok = svc.GetByName(context.Background(), "nonexistent")
	assert.False(t, ok)
}

func TestCatalogService_EnvVars(t *testing.T) {
	svc := NewCatalogService(fixtureRepo())

	entry, ok := svc.GetByName(context.Background(), "tavily-web-search")
	require.True(t, ok)
	require.Len(t, entry.Packages[0].EnvVars, 1)

	env := entry.Packages[0].EnvVars[0]
	assert.Equal(t, "TAVILY_API_KEY", env.Name)
	assert.True(t, env.Required)
	assert.True(t, env.Secret)
}
