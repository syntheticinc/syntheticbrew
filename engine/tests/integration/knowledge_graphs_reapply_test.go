//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestKG_ReApplyAfterRegistryWarm_NoSelfCollision reproduces the 1.4.0 bug
// fixed in 1.4.1 at the full HTTP layer.
//
// A design partner's CI re-applied the same bundle on every deploy and hit
// HTTP 409 [ALREADY_EXISTS] tool name collision listing the bundle's OWN
// tools. The trigger was a warmed in-memory tool registry: any agent bound
// to the bundle, once its DerivedTools were computed (on agent-registry
// Load), populated the kgtools registry for that bundle. The collision
// detector's RegistryToolNames source then reported the bundle's own cached
// tools as pre-existing during its own re-apply, because it ignored the
// excludeBundle argument the detector passed in.
//
// Reproduction sequence (matches the partner's real flow):
//  1. apply bundle            -> 200 (registry not yet warm for this bundle)
//  2. bind it to an agent     -> capability config saved
//  3. POST /config/reload     -> AgentRegistry.Load -> DeriveRuntimeTools ->
//     KG capability ResolveToolsForBundles -> warms the kgtools registry for
//     the bundle (the step the partner's chat traffic performed implicitly)
//  4. re-apply the same bundle
//
// Before the fix step 4 returned 409. After the fix it returns 200 and apply
// is idempotent for the same bundle_name, as documented.
func TestKG_ReApplyAfterRegistryWarm_NoSelfCollision(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	bundleName := "tc-kg-reapply-warm"

	// 1. First apply — succeeds, no collision (nothing warmed yet).
	first := bulkImportPayload("1.0.0", []map[string]any{
		{"code": "FW", "name": "Footwear", "popularity": "high"},
	})
	applyResp := do(t, http.MethodPost,
		"/api/v1/knowledge-graphs/"+bundleName+"/import",
		mustJSON(first), adminToken)
	require.Equalf(t, http.StatusOK, applyResp.StatusCode,
		"first apply must succeed, body=%s", readBody(t, applyResp))

	// 2. Bind the bundle to an agent via the knowledge_graphs capability.
	agentName := "tc-kg-reapply-agent"
	_ = createAgentForTest(t, agentName)
	capResp := do(t, http.MethodPost,
		"/api/v1/agents/"+agentName+"/capabilities",
		mustJSON(map[string]any{
			"type":    "knowledge_graphs",
			"enabled": true,
			"config":  map[string]any{"bundles": []string{bundleName}},
		}), adminToken)
	capBody := readBody(t, capResp)
	require.Truef(t,
		capResp.StatusCode == http.StatusOK || capResp.StatusCode == http.StatusCreated,
		"capability bind failed: status=%d body=%s", capResp.StatusCode, capBody)

	// 3. Warm the registry: a config reload recomputes every agent's
	//    DerivedTools, which resolves the KG capability's tools and populates
	//    the in-memory kgtools registry for this bundle — exactly what the
	//    partner's chat traffic did before the second apply.
	reloadResp := do(t, http.MethodPost, "/api/v1/config/reload", nil, adminToken)
	reloadBody := readBody(t, reloadResp)
	require.Equalf(t, http.StatusOK, reloadResp.StatusCode,
		"config reload must succeed (warms kgtools registry), body=%s", reloadBody)

	// 4. Re-apply the SAME bundle with an edited entity set. Before 1.4.1
	//    this returned 409 on the bundle's own tool names. It must now be a
	//    clean idempotent upsert.
	second := bulkImportPayload("1.0.1", []map[string]any{
		{"code": "FW", "name": "Footwear", "popularity": "high"},
		{"code": "AP", "name": "Apparel", "popularity": "medium"},
	})
	reapplyResp := do(t, http.MethodPost,
		"/api/v1/knowledge-graphs/"+bundleName+"/import",
		mustJSON(second), adminToken)
	reapplyBody := readBody(t, reapplyResp)
	require.Equalf(t, http.StatusOK, reapplyResp.StatusCode,
		"re-apply of the same warmed bundle must be idempotent (no self-collision), got status=%d body=%s",
		reapplyResp.StatusCode, reapplyBody)

	// The edited entity must have landed — proves the upsert actually ran,
	// not just that the collision check was skipped.
	listResp := do(t, http.MethodGet,
		"/api/v1/knowledge-graphs/"+bundleName+"/entities/category?filter[code]=AP",
		nil, adminToken)
	listBody := readBody(t, listResp)
	require.Equalf(t, http.StatusOK, listResp.StatusCode,
		"list after re-apply must succeed, body=%s", listBody)
	require.Containsf(t, string(listBody), "Apparel",
		"re-applied entity must be queryable, body=%s", listBody)
}
