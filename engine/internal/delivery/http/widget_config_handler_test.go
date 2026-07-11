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

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

type fakeWidgetPolicyReader struct {
	policy *domain.TenantPolicy
	err    error
}

func (f fakeWidgetPolicyReader) Get(ctx context.Context, key string) (*domain.TenantPolicy, error) {
	return f.policy, f.err
}

func TestWidgetConfigHandler_Get(t *testing.T) {
	tests := []struct {
		name     string
		handler  *WidgetConfigHandler
		wantAttr bool
	}{
		{
			name:     "attribution on → true",
			handler:  NewWidgetConfigHandler(fakeWidgetPolicyReader{policy: &domain.TenantPolicy{Key: domain.PolicyWidgetAttribution, Value: "on"}}),
			wantAttr: true,
		},
		{
			name:     "attribution off → false",
			handler:  NewWidgetConfigHandler(fakeWidgetPolicyReader{policy: &domain.TenantPolicy{Key: domain.PolicyWidgetAttribution, Value: "off"}}),
			wantAttr: false,
		},
		{
			name:     "absent policy row → false",
			handler:  NewWidgetConfigHandler(fakeWidgetPolicyReader{policy: nil}),
			wantAttr: false,
		},
		{
			name:     "read error → false (fail-quiet)",
			handler:  NewWidgetConfigHandler(fakeWidgetPolicyReader{err: errors.New("db down")}),
			wantAttr: false,
		},
		{
			name:     "nil reader → false",
			handler:  NewWidgetConfigHandler(nil),
			wantAttr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tt.handler.Get(rec, httptest.NewRequest(http.MethodGet, "/api/v1/widget-config", nil))

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "private, max-age=300", rec.Header().Get("Cache-Control"),
				"widget-config must be privately cacheable")

			var got WidgetConfigResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, tt.wantAttr, got.Attribution)
		})
	}
}
