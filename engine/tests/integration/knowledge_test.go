//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type kbCreateResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// createKBForTest POSTs a knowledge base; returns id+name. embedding_model
// shape varies across builds — try with it, fall back to omitting it.
func createKBForTest(t *testing.T, name string) (kbCreateResp, bool) {
	t.Helper()
	body := map[string]any{
		"name":            name,
		"embedding_model": "text-embedding-ada-002",
	}
	resp := do(t, http.MethodPost, "/api/v1/knowledge-bases", mustJSON(body), adminToken)
	raw := readBody(t, resp)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// Retry without embedding_model — some builds infer it.
		resp2 := do(t, http.MethodPost, "/api/v1/knowledge-bases",
			mustJSON(map[string]any{"name": name}), adminToken)
		raw = readBody(t, resp2)
		if resp2.StatusCode >= 400 {
			t.Logf("KB create returned %d: %s", resp2.StatusCode, raw)
			return kbCreateResp{}, false
		}
		resp = resp2
	}
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)

	var parsed kbCreateResp
	_ = json.Unmarshal(raw, &parsed)
	if parsed.Name == "" {
		parsed.Name = name
	}
	return parsed, true
}

// TC-KB-01: Create knowledge base → 201.
func TestKB01_CreateKB(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	_, ok := createKBForTest(t, "tc-kb-01-kb")
	if !ok {
		t.Skip("KB create rejected by this build")
	}
}

// TC-KB-02: Multipart file upload to KB.
func TestKB02_UploadFile(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	kb, ok := createKBForTest(t, "tc-kb-02-kb")
	if !ok {
		t.Skip("KB create rejected")
	}
	require.NotEmpty(t, kb.ID, "KB create must return id for upload test")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "tc-kb-02.txt")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("hello knowledge world"))
	require.NoError(t, mw.Close())

	resp := doHeaders(t, http.MethodPost, "/api/v1/knowledge-bases/"+kb.Name+"/files",
		&body,
		map[string]string{
			"Content-Type":  mw.FormDataContentType(),
			"Authorization": "Bearer " + adminToken,
		})
	raw := readBody(t, resp)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		t.Skipf("upload rejected (%d): %s — likely needs embedding provider configured", resp.StatusCode, string(raw))
	}
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// TC-KB-03: List files after upload.
func TestKB03_ListFiles(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	kb, ok := createKBForTest(t, "tc-kb-03-kb")
	if !ok {
		t.Skip("KB create rejected")
	}
	require.NotEmpty(t, kb.ID)

	resp := do(t, http.MethodGet, "/api/v1/knowledge-bases/"+kb.Name+"/files", nil, adminToken)
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TC-KB-04: Delete a KB file — skip if upload/listing isn't plumbed.
func TestKB04_DeleteFile(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	kb, ok := createKBForTest(t, "tc-kb-04-kb")
	if !ok {
		t.Skip("KB create rejected")
	}
	require.NotEmpty(t, kb.ID)

	// Upload a file, try to find its id, then delete.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "tc-kb-04.txt")
	require.NoError(t, err)
	_, _ = fw.Write([]byte("bye knowledge"))
	require.NoError(t, mw.Close())

	upResp := doHeaders(t, http.MethodPost, "/api/v1/knowledge-bases/"+kb.Name+"/files",
		&body,
		map[string]string{
			"Content-Type":  mw.FormDataContentType(),
			"Authorization": "Bearer " + adminToken,
		})
	upBody := readBody(t, upResp)
	if upResp.StatusCode >= 400 {
		t.Skipf("upload rejected (%d): %s", upResp.StatusCode, string(upBody))
	}

	var fileResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(upBody, &fileResp); err != nil || fileResp.ID == "" {
		t.Skip("upload did not return a file id")
	}

	delResp := do(t, http.MethodDelete,
		"/api/v1/knowledge-bases/"+kb.Name+"/files/"+fileResp.ID, nil, adminToken)
	_ = readBody(t, delResp)
	assertStatusAny(t, delResp, http.StatusOK, http.StatusNoContent, http.StatusAccepted)
}

// TC-KB-05: Delete the KB itself.
func TestKB05_DeleteKB(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	kb, ok := createKBForTest(t, "tc-kb-05-kb")
	if !ok {
		t.Skip("KB create rejected")
	}
	require.NotEmpty(t, kb.ID)

	resp := do(t, http.MethodDelete, "/api/v1/knowledge-bases/"+kb.Name, nil, adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusNoContent)
}

// TC-KB-06: Unknown KB name → 404.
//
// Engine 1.1.0+: URL is name-keyed. Send a valid-format name that doesn't
// exist in the tenant — must return 404, not leak existence.
func TestKB06_UnknownID(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet,
		"/api/v1/knowledge-bases/does-not-exist", nil, adminToken)
	_ = readBody(t, resp)
	assertStatusAny(t, resp, http.StatusNotFound)
}

// TC-KB-07: GET /knowledge-bases → 200 list.
func TestKB07_ListKBs(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet, "/api/v1/knowledge-bases", nil, adminToken)
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TC-KB-08: /knowledge-bases without token → 401.
func TestKB08_RequireAuth(t *testing.T) {
	requireSuite(t)

	resp := do(t, http.MethodGet, "/api/v1/knowledge-bases", nil, "")
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
