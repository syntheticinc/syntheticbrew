//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedEmbeddingModelForKB creates an embedding model so a KB upload passes its
// embedding-model guard and returns 2xx. The base_url is never reached
// synchronously (async indexing fails later, which is irrelevant to the
// file_name assertions below).
func seedEmbeddingModelForKB(t *testing.T, name string) string {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/models",
		mustJSON(map[string]any{
			"name":          name,
			"type":          "openai_compatible",
			"kind":          "embedding",
			"base_url":      "https://api.openai.com/v1",
			"model_name":    "text-embedding-3-small",
			"api_key":       "sk-test-embed",
			"embedding_dim": 1536,
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)
	var m struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(body, &m), "parse embed model: %s", body)
	require.NotEmpty(t, m.ID)
	return m.ID
}

func createKBWithEmbedding(t *testing.T, name, embeddingModelID string) kbCreateResp {
	t.Helper()
	resp := do(t, http.MethodPost, "/api/v1/knowledge-bases",
		mustJSON(map[string]any{
			"name":               name,
			"embedding_model_id": embeddingModelID,
		}), adminToken)
	body := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated)
	var kb kbCreateResp
	require.NoError(t, json.Unmarshal(body, &kb), "parse kb: %s", body)
	if kb.Name == "" {
		kb.Name = name
	}
	return kb
}

// uploadKBFile posts a text file with an explicit text/plain part Content-Type
// (multipart.CreateFormFile would default to application/octet-stream, which the
// .txt MIME allow-list rejects).
func uploadKBFile(t *testing.T, kbName, fileName, content string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, fileName))
	hdr.Set("Content-Type", "text/plain")
	fw, err := mw.CreatePart(hdr)
	require.NoError(t, err)
	_, _ = fw.Write([]byte(content))
	require.NoError(t, mw.Close())
	return doHeaders(t, http.MethodPost, "/api/v1/knowledge-bases/"+kbName+"/files",
		&body, map[string]string{
			"Content-Type":  mw.FormDataContentType(),
			"Authorization": "Bearer " + adminToken,
		})
}

// TC-KB-09: the uploaded file name is stored as metadata and displayed both in
// the upload response and the file list — regression guard for the stateless
// "." bug (FilePath empty → filepath.Base("")==".").
func TestKB09_FileNameStoredAndDisplayed(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	embedID := seedEmbeddingModelForKB(t, "tc-kb-09-embed")
	kb := createKBWithEmbedding(t, "tc-kb-09-kb", embedID)
	require.NotEmpty(t, kb.ID)

	const fileName = "quarterly-report.txt"
	resp := uploadKBFile(t, kb.Name, fileName, "hello knowledge world")
	raw := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated, http.StatusAccepted)

	var up struct {
		ID       string `json:"id"`
		FileName string `json:"file_name"`
	}
	require.NoError(t, json.Unmarshal(raw, &up), "parse upload: %s", raw)
	assert.Equal(t, fileName, up.FileName, "upload response must echo the original file name")

	listResp := do(t, http.MethodGet, "/api/v1/knowledge-bases/"+kb.Name+"/files", nil, adminToken)
	listRaw := readBody(t, listResp)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var files []struct {
		FileName string `json:"file_name"`
	}
	require.NoError(t, json.Unmarshal(listRaw, &files), "parse list: %s", listRaw)
	require.NotEmpty(t, files, "uploaded file must appear in the list")
	for _, f := range files {
		assert.Equal(t, fileName, f.FileName)
		assert.NotEqual(t, ".", f.FileName, `file name must never render as "."`)
		assert.NotEmpty(t, f.FileName)
	}
}

// TC-KB-10: the re-index endpoints were removed (stateless model) → 404.
func TestKB10_ReindexEndpointsGone(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	embedID := seedEmbeddingModelForKB(t, "tc-kb-10-embed")
	kb := createKBWithEmbedding(t, "tc-kb-10-kb", embedID)

	resp := uploadKBFile(t, kb.Name, "doc.txt", "content")
	upRaw := readBody(t, resp)
	assertStatusAny(t, resp, http.StatusOK, http.StatusCreated, http.StatusAccepted)
	var up struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(upRaw, &up)

	r1 := do(t, http.MethodPost,
		"/api/v1/knowledge-bases/"+kb.Name+"/files/"+up.ID+"/reindex", nil, adminToken)
	_ = readBody(t, r1)
	assert.Equal(t, http.StatusNotFound, r1.StatusCode, "KB-scoped reindex endpoint must be removed")

	r2 := do(t, http.MethodPost, "/api/v1/agents/any-agent/knowledge/reindex", nil, adminToken)
	_ = readBody(t, r2)
	assert.Equal(t, http.StatusNotFound, r2.StatusCode, "agent-scoped reindex endpoint must be removed")

	r3 := do(t, http.MethodPost,
		"/api/v1/agents/any-agent/knowledge/files/some-id/reindex", nil, adminToken)
	_ = readBody(t, r3)
	assert.Equal(t, http.StatusNotFound, r3.StatusCode, "agent-scoped file reindex endpoint must be removed")
}

// TC-KB-11 (SCC-03): an unsupported file type is rejected with 400, not 500.
func TestKB11_UnsupportedTypeRejected(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	embedID := seedEmbeddingModelForKB(t, "tc-kb-11-embed")
	kb := createKBWithEmbedding(t, "tc-kb-11-kb", embedID)

	resp := uploadKBFile(t, kb.Name, "malware.exe", "MZ binary")
	_ = readBody(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"unsupported file type must be 400, not 500 (SCC-03)")
}

// TC-KB-12: an in-place upgrade from a pre-1.9.0 (stateful) deployment must keep
// showing correct names for ALREADY-uploaded knowledge. Pre-1.9.0 rows have
// file_path set (the raw file used to be persisted) and, after migration 014
// adds file_name with DEFAULT '', file_name=''. FileName() must fall back to the
// path basename — NOT "." and NOT empty. This is the document-side guarantee of
// the stateful->stateless upgrade path.
func TestKB12_LegacyRowDisplaysViaBasenameFallback(t *testing.T) {
	requireSuite(t)
	t.Cleanup(func() { truncateTables(t) })

	embedID := seedEmbeddingModelForKB(t, "tc-kb-12-embed")
	kb := createKBWithEmbedding(t, "tc-kb-12-kb", embedID)
	require.NotEmpty(t, kb.ID)

	// Insert a legacy document row directly: file_path set, file_name='' (the
	// exact state of an existing row after migration 014 runs on upgrade).
	ctx := context.Background()
	const docID = "11111111-1111-1111-1111-111111111111"
	legacyPath := "/data/knowledge/00000000-0000-0000-0000-000000000001/" + kb.ID + "/8f3a1b2c_legacy-report.pdf"
	err := testDB.WithContext(ctx).Exec(
		`INSERT INTO knowledge_documents
		   (id, knowledge_base_id, tenant_id, file_path, file_name, file_type, file_size, file_hash, status, chunk_count, created_at, updated_at)
		 VALUES (?, ?, '00000000-0000-0000-0000-000000000001', ?, '', 'pdf', 1024, 'deadbeef', 'ready', 3, now(), now())`,
		docID, kb.ID, legacyPath).Error
	require.NoError(t, err, "seed legacy knowledge_documents row")

	listResp := do(t, http.MethodGet, "/api/v1/knowledge-bases/"+kb.Name+"/files", nil, adminToken)
	listRaw := readBody(t, listResp)
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	var files []struct {
		ID       string `json:"id"`
		FileName string `json:"file_name"`
	}
	require.NoError(t, json.Unmarshal(listRaw, &files), "parse list: %s", listRaw)

	var found bool
	for _, f := range files {
		if f.ID == docID {
			found = true
			assert.Equal(t, "8f3a1b2c_legacy-report.pdf", f.FileName,
				"legacy row must display the FilePath basename")
			assert.NotEqual(t, ".", f.FileName)
			assert.NotEmpty(t, f.FileName)
		}
	}
	require.True(t, found, "seeded legacy document must appear in the file list")
}
