package app

import (
	"context"
	"strings"
	"testing"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/llm"
)

type fakeUsageGate struct {
	dec              domain.UsageDecision
	checkErr         error
	checkCalls       int
	recordCalls      int
	recordSteps      int
	recordStepsCalls int
	recordStepsSteps int
}

func (f *fakeUsageGate) CheckAllowed(context.Context, string) (domain.UsageDecision, error) {
	f.checkCalls++
	return f.dec, f.checkErr
}

func (f *fakeUsageGate) RecordTurn(_ context.Context, _ string, steps int) error {
	f.recordCalls++
	f.recordSteps = steps
	return nil
}

func (f *fakeUsageGate) RecordSteps(_ context.Context, _ string, steps int) error {
	f.recordStepsCalls++
	f.recordStepsSteps = steps
	return nil
}

type fakeStepTaker struct {
	steps        int
	beginCalls   int
	takeCalls    int
	discardCalls int
}

func (f *fakeStepTaker) Begin(string)    { f.beginCalls++ }
func (f *fakeStepTaker) Take(string) int { f.takeCalls++; return f.steps }
func (f *fakeStepTaker) Discard(string)  { f.discardCalls++ }

// --- gateTurn ---

func TestGateTurn_AllowsUnderLimit(t *testing.T) {
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: true}}
	a := &chatServiceHTTPAdapter{usage: g}
	if err := a.gateTurn(context.Background(), "user-1", false); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if g.checkCalls != 1 {
		t.Fatalf("want 1 check, got %d", g.checkCalls)
	}
}

func TestGateTurn_BlocksAtLimit_UsageLimited(t *testing.T) {
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: false, BlockedScope: domain.ScopeTenant, Unit: domain.UnitTurns, Limit: 50, Used: 50}}
	a := &chatServiceHTTPAdapter{usage: g}
	err := a.gateTurn(context.Background(), "user-1", false)
	if err == nil || !strings.Contains(err.Error(), "usage limit reached") {
		t.Fatalf("want usage-limit error, got %v", err)
	}
}

func TestGateTurn_BYOKSkips(t *testing.T) {
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: false}} // would block if consulted
	a := &chatServiceHTTPAdapter{usage: g}
	if err := a.gateTurn(context.Background(), "user-1", true); err != nil {
		t.Fatalf("BYOK must skip gate, got %v", err)
	}
	if g.checkCalls != 0 {
		t.Fatalf("BYOK must not consult gate, got %d calls", g.checkCalls)
	}
}

func TestGateTurn_NilUsageIsNoOp(t *testing.T) {
	a := &chatServiceHTTPAdapter{}
	if err := a.gateTurn(context.Background(), "user-1", false); err != nil {
		t.Fatalf("nil usage must be no-op, got %v", err)
	}
}

// gateTurn must recognise a real BYOK context (integration of the detection
// used by Chat) as skipping the gate.
func TestGateTurn_RealBYOKContextSkips(t *testing.T) {
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: false}}
	a := &chatServiceHTTPAdapter{usage: g}
	ctx := llm.WithBYOKCredentials(context.Background(), &llm.BYOKCredentials{Provider: "openai", APIKey: "sk-test", Model: "gpt-4o"})
	byok := llm.BYOKCredentialsFrom(ctx) != nil
	if !byok {
		t.Fatal("expected BYOK detected in context")
	}
	if err := a.gateTurn(ctx, "user-1", byok); err != nil {
		t.Fatalf("real BYOK ctx must skip gate, got %v", err)
	}
}

// --- settleTurn ---

func TestSettleTurn_BillableRecordsWithSteps(t *testing.T) {
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 7}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc}
	sawOutput := true
	a.settleTurn(context.Background(), "sess-1", "user-1", false, &sawOutput)
	if g.recordCalls != 1 || g.recordSteps != 7 {
		t.Fatalf("want RecordTurn(1x, steps=7), got calls=%d steps=%d", g.recordCalls, g.recordSteps)
	}
	if acc.takeCalls != 1 || acc.discardCalls != 0 {
		t.Fatalf("billable must Take not Discard, got take=%d discard=%d", acc.takeCalls, acc.discardCalls)
	}
}

func TestSettleTurn_NoOutputDiscards(t *testing.T) {
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 3}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc}
	sawOutput := false
	a.settleTurn(context.Background(), "sess-1", "user-1", false, &sawOutput)
	if g.recordCalls != 0 {
		t.Fatalf("non-billable must NOT record, got %d", g.recordCalls)
	}
	if acc.discardCalls != 1 || acc.takeCalls != 0 {
		t.Fatalf("non-billable must Discard not Take, got discard=%d take=%d", acc.discardCalls, acc.takeCalls)
	}
}

func TestSettleTurn_BYOKSkips(t *testing.T) {
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 5}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc}
	sawOutput := true
	a.settleTurn(context.Background(), "sess-1", "user-1", true, &sawOutput)
	if g.recordCalls != 0 || acc.takeCalls != 0 || acc.discardCalls != 0 {
		t.Fatalf("BYOK settle must be fully skipped, got record=%d take=%d discard=%d", g.recordCalls, acc.takeCalls, acc.discardCalls)
	}
}

func TestSettleResumeSteps_RecordsStepsNotTurn(t *testing.T) {
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 9}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc}
	a.settleResumeSteps(context.Background(), "sess-1", "user-1", false)
	if g.recordStepsCalls != 1 || g.recordStepsSteps != 9 {
		t.Fatalf("resume must RecordSteps(9), got calls=%d steps=%d", g.recordStepsCalls, g.recordStepsSteps)
	}
	if g.recordCalls != 0 {
		t.Fatalf("resume must NOT RecordTurn, got %d", g.recordCalls)
	}
	if acc.takeCalls != 1 {
		t.Fatalf("resume must Take once, got %d", acc.takeCalls)
	}
}

func TestSettleResumeSteps_BYOKSkips(t *testing.T) {
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 9}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc}
	a.settleResumeSteps(context.Background(), "sess-1", "user-1", true)
	if g.recordStepsCalls != 0 || acc.takeCalls != 0 {
		t.Fatalf("BYOK resume must skip settle, got recordSteps=%d take=%d", g.recordStepsCalls, acc.takeCalls)
	}
}

// --- active-users gate ---

type fakeActiveUsersGate struct {
	dec         domain.ActiveUsersDecision
	checkErr    error
	checkCalls  int
	recordCalls int
	recordSubs  []string
	recordErr   error
}

func (f *fakeActiveUsersGate) Check(context.Context, string) (domain.ActiveUsersDecision, error) {
	f.checkCalls++
	return f.dec, f.checkErr
}

func (f *fakeActiveUsersGate) RecordActivity(_ context.Context, userSub string) error {
	f.recordCalls++
	f.recordSubs = append(f.recordSubs, userSub)
	return f.recordErr
}

func TestGateTurn_ActiveUsersCheckRunsForBYOK(t *testing.T) {
	au := &fakeActiveUsersGate{dec: domain.ActiveUsersDecision{Allowed: true}}
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: false}} // must not be consulted for BYOK
	a := &chatServiceHTTPAdapter{usage: g, activeUsers: au}
	if err := a.gateTurn(context.Background(), "user-1", true); err != nil {
		t.Fatalf("BYOK turn under user limit must pass, got %v", err)
	}
	if au.checkCalls != 1 {
		t.Fatalf("active-users check must run for BYOK, got %d calls", au.checkCalls)
	}
	if g.checkCalls != 0 {
		t.Fatalf("BYOK must not consult usage gate, got %d calls", g.checkCalls)
	}
}

func TestGateTurn_ActiveUsersRejectionMessage(t *testing.T) {
	au := &fakeActiveUsersGate{dec: domain.ActiveUsersDecision{Allowed: false, Limit: 10, Used: 10}}
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: true}}
	a := &chatServiceHTTPAdapter{usage: g, activeUsers: au}
	err := a.gateTurn(context.Background(), "user-new", false)
	if err == nil || !strings.Contains(err.Error(), "user limit reached") {
		t.Fatalf("want user-limit error, got %v", err)
	}
	if !strings.Contains(err.Error(), "10/10") {
		t.Fatalf("want used/limit in message, got %v", err)
	}
	if g.checkCalls != 0 {
		t.Fatalf("user-limit rejection must short-circuit usage gate, got %d calls", g.checkCalls)
	}
}

func TestGateTurn_ActiveUsersRejectsBYOKToo(t *testing.T) {
	au := &fakeActiveUsersGate{dec: domain.ActiveUsersDecision{Allowed: false, Limit: 10, Used: 10}}
	a := &chatServiceHTTPAdapter{activeUsers: au}
	err := a.gateTurn(context.Background(), "user-new", true)
	if err == nil || !strings.Contains(err.Error(), "user limit reached") {
		t.Fatalf("BYOK must not bypass user limit, got %v", err)
	}
}

func TestGateTurn_NilActiveUsersIsNoOp(t *testing.T) {
	g := &fakeUsageGate{dec: domain.UsageDecision{Allowed: true}}
	a := &chatServiceHTTPAdapter{usage: g}
	if err := a.gateTurn(context.Background(), "user-1", false); err != nil {
		t.Fatalf("nil activeUsers must be no-op, got %v", err)
	}
	if g.checkCalls != 1 {
		t.Fatalf("usage gate must still run, got %d calls", g.checkCalls)
	}
}

func TestSettleTurn_RecordsActivityOnOutput(t *testing.T) {
	au := &fakeActiveUsersGate{}
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 2}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc, activeUsers: au}
	sawOutput := true
	a.settleTurn(context.Background(), "sess-1", "user-1", false, &sawOutput)
	if au.recordCalls != 1 || au.recordSubs[0] != "user-1" {
		t.Fatalf("want RecordActivity(user-1) once, got calls=%d subs=%v", au.recordCalls, au.recordSubs)
	}
}

func TestSettleTurn_NoOutputDoesNotRecordActivity(t *testing.T) {
	au := &fakeActiveUsersGate{}
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc, activeUsers: au}
	sawOutput := false
	a.settleTurn(context.Background(), "sess-1", "user-1", false, &sawOutput)
	if au.recordCalls != 0 {
		t.Fatalf("failed turn must NOT record activity, got %d calls", au.recordCalls)
	}
}

func TestSettleTurn_BYOKStillRecordsActivity(t *testing.T) {
	au := &fakeActiveUsersGate{}
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 5}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc, activeUsers: au}
	sawOutput := true
	a.settleTurn(context.Background(), "sess-1", "user-1", true, &sawOutput)
	if au.recordCalls != 1 {
		t.Fatalf("BYOK turn with output must record activity, got %d calls", au.recordCalls)
	}
	if g.recordCalls != 0 || acc.takeCalls != 0 {
		t.Fatalf("BYOK step settle must stay skipped, got record=%d take=%d", g.recordCalls, acc.takeCalls)
	}
}

func TestSettleTurn_NilActiveUsersNoPanic(t *testing.T) {
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 3}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc}
	sawOutput := true
	a.settleTurn(context.Background(), "sess-1", "user-1", false, &sawOutput)
	if g.recordCalls != 1 {
		t.Fatalf("usage settle must still run with nil activeUsers, got %d", g.recordCalls)
	}
}

func TestSettleResumeSteps_RecordsActivity(t *testing.T) {
	au := &fakeActiveUsersGate{}
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 4}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc, activeUsers: au}
	a.settleResumeSteps(context.Background(), "sess-1", "user-1", false)
	if au.recordCalls != 1 || au.recordSubs[0] != "user-1" {
		t.Fatalf("resume must record activity, got calls=%d subs=%v", au.recordCalls, au.recordSubs)
	}
}

func TestSettleResumeSteps_BYOKStillRecordsActivity(t *testing.T) {
	au := &fakeActiveUsersGate{}
	g := &fakeUsageGate{}
	acc := &fakeStepTaker{steps: 4}
	a := &chatServiceHTTPAdapter{usage: g, accumulator: acc, activeUsers: au}
	a.settleResumeSteps(context.Background(), "sess-1", "user-1", true)
	if au.recordCalls != 1 {
		t.Fatalf("BYOK resume must record activity, got %d calls", au.recordCalls)
	}
	if g.recordStepsCalls != 0 {
		t.Fatalf("BYOK resume step settle must stay skipped, got %d", g.recordStepsCalls)
	}
}
