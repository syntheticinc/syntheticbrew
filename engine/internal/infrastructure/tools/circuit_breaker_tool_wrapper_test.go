package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/syntheticinc/syntheticbrew/pkg/errors"
)

// stubInnerTool is a minimal InvokableTool for CB wrapper tests.
type stubInnerTool struct {
	name   string
	output string
	err    error
	calls  int
}

func (s *stubInnerTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: s.name}, nil
}

func (s *stubInnerTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	s.calls++
	return s.output, s.err
}

var _ tool.InvokableTool = (*stubInnerTool)(nil)

// stubBreaker lets each test fix the Allow/Record behavior independently.
type stubBreaker struct {
	allowErr     error
	successCalls int
	failureCalls int
}

func (b *stubBreaker) AllowRequest() error { return b.allowErr }
func (b *stubBreaker) RecordSuccess()      { b.successCalls++ }
func (b *stubBreaker) RecordFailure()      { b.failureCalls++ }

func TestCircuitBreakerToolWrapper_AllowRequestErr_ReturnsError(t *testing.T) {
	// CB-open must return a Go error so Eino's OnToolError fires and the
	// session_event_log records has_error=true. Returning nil would make
	// the Tool Call Log show the failed call as "completed".
	inner := &stubInnerTool{name: "brave_search"}
	// Real AllowRequest returns a typed Unavailable DomainError whose Error()
	// carries the "[UNAVAILABLE]" prefix; the wrapper forwards it as-is.
	breaker := &stubBreaker{allowErr: apperrors.Unavailable("service unavailable", errors.New("circuit open"))}

	w := NewCircuitBreakerToolWrapper(inner, breaker)
	output, err := w.InvokableRun(context.Background(), `{}`)

	require.Error(t, err, "CB-open must surface as Go error, not a nil-err string payload")
	assert.Empty(t, output, "output should be empty when CB short-circuits")
	assert.True(t, strings.Contains(err.Error(), "[UNAVAILABLE]"),
		"error message must keep the [UNAVAILABLE] prefix for log filtering")
	assert.Equal(t, apperrors.CodeUnavailable, apperrors.DeepestCode(err),
		"CB-open error must carry the typed UNAVAILABLE code")
	assert.Equal(t, 0, inner.calls, "inner tool must not be invoked when CB is open")
	assert.Equal(t, 0, breaker.successCalls)
	assert.Equal(t, 0, breaker.failureCalls)
}

func TestCircuitBreakerToolWrapper_InnerError_RecordsFailure(t *testing.T) {
	inner := &stubInnerTool{name: "brave_search", err: errors.New("boom")}
	breaker := &stubBreaker{}

	w := NewCircuitBreakerToolWrapper(inner, breaker)
	_, err := w.InvokableRun(context.Background(), `{}`)

	require.Error(t, err)
	assert.Equal(t, 1, inner.calls)
	assert.Equal(t, 1, breaker.failureCalls)
	assert.Equal(t, 0, breaker.successCalls)
}

func TestCircuitBreakerToolWrapper_InnerSuccess_RecordsSuccess(t *testing.T) {
	inner := &stubInnerTool{name: "brave_search", output: "ok"}
	breaker := &stubBreaker{}

	w := NewCircuitBreakerToolWrapper(inner, breaker)
	out, err := w.InvokableRun(context.Background(), `{}`)

	require.NoError(t, err)
	assert.Equal(t, "ok", out)
	assert.Equal(t, 1, inner.calls)
	assert.Equal(t, 1, breaker.successCalls)
	assert.Equal(t, 0, breaker.failureCalls)
}

func TestCircuitBreakerToolWrapper_Info_Delegates(t *testing.T) {
	inner := &stubInnerTool{name: "brave_search"}
	w := NewCircuitBreakerToolWrapper(inner, &stubBreaker{})
	info, err := w.Info(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "brave_search", info.Name)
}
