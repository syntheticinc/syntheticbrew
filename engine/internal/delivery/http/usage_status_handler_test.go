package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgerrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

type fakeUsageStatusProvider struct {
	resp UsageStatusResponse
	err  error
}

func (f *fakeUsageStatusProvider) UsageStatus(ctx context.Context) (UsageStatusResponse, error) {
	return f.resp, f.err
}

func int64Ptr(v int64) *int64 { return &v }

func TestUsageStatusHandler_Get_JSONShape(t *testing.T) {
	provider := &fakeUsageStatusProvider{
		resp: UsageStatusResponse{
			ActiveUsers:        UsageStatusMetric{Used: 3, Limit: int64Ptr(10)},
			Schemas:            UsageStatusMetric{Used: 2, Limit: nil},
			KnowledgeDocuments: UsageStatusMetric{Used: 5, Limit: int64Ptr(100)},
			Turns:              UsageStatusMetric{Used: 7, Limit: nil},
		},
	}
	h := NewUsageStatusHandler(provider)

	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, "/api/v1/usage-status", nil))

	require.Equal(t, http.StatusOK, rec.Code)

	// Null limits must serialize as JSON null, not 0.
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.JSONEq(t, `{"used":2,"limit":null}`, string(raw["schemas"]))
	assert.JSONEq(t, `{"used":7,"limit":null}`, string(raw["turns"]))

	var got UsageStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, int64(3), got.ActiveUsers.Used)
	require.NotNil(t, got.ActiveUsers.Limit)
	assert.Equal(t, int64(10), *got.ActiveUsers.Limit)
	assert.Nil(t, got.Schemas.Limit)
	require.NotNil(t, got.KnowledgeDocuments.Limit)
	assert.Equal(t, int64(100), *got.KnowledgeDocuments.Limit)
	assert.Nil(t, got.Turns.Limit)
}

func TestUsageStatusHandler_Get_ErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"domain invalid input → 400", pkgerrors.InvalidInput("bad"), http.StatusBadRequest},
		{"plain error → 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewUsageStatusHandler(&fakeUsageStatusProvider{err: tt.err})
			rec := httptest.NewRecorder()
			h.Get(rec, httptest.NewRequest(http.MethodGet, "/api/v1/usage-status", nil))
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}
