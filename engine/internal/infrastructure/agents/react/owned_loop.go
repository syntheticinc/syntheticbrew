package react

import (
	"context"
	"io"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/agents/callbacks"
)

// owned_loop.go builds our own ReAct execution graph instead of delegating to
// eino's prebuilt flow/agent/react.Agent. Owning the graph lets us route a
// budget-exhausted or loop-stuck turn into a dedicated finalize node — a model
// call with the tools removed — so the model is structurally forced to write a
// summary from the gathered context, rather than emitting a hardcoded apology
// and scrubbing what it found.
//
// The graph mirrors eino's react template (chat ⇄ tools, with a direct-return
// branch for HITL) and adds a finalize node plus reason-aware routing. State is
// ours, so the assistant message keeps its content alongside tool_calls and the
// breaker/budget counters live in the loop rather than being reconstructed from
// callbacks.

// Graph node keys.
const (
	ownedNodeChat           = "chat"
	ownedNodeTools          = "tools"
	ownedNodeFinalize       = "finalize"
	ownedNodeDirectReturn   = "direct_return"
	ownedNodeChatToFinalize = "chat_to_finalize"
)

// ownedState is the loop state, owned by us (eino's react state is private). It
// carries the running transcript plus the routing signals the branches read.
type ownedState struct {
	// Messages is the running transcript: input, every assistant message
	// (content AND tool_calls preserved together), and every tool result.
	Messages []*schema.Message

	// ReturnDirectlyToolCallID mirrors eino: when a HITL/return-directly tool
	// fires, its call ID is recorded so the tools branch routes to direct_return.
	ReturnDirectlyToolCallID string

	// step counts completed ReAct steps (model call + its tools). turnStart
	// anchors the wall-clock budget. Both drive soft-landing / finalize routing.
	step      int
	turnStart time.Time

	// terminalReason records why the turn is being force-finalized (budget wall,
	// or an escalated loop). TerminalNone means the turn is proceeding normally.
	terminalReason callbacks.TerminalReason
	terminalDetail string
	terminalTool   string

	// Loop-breaker counters (graph-owned, replacing the callbacks-layer breaker).
	consecErrByTool map[string]int // per-tool consecutive [ERROR] streak
	lastArgsKey     string         // last single-tool call key (name\x00args)
	sameArgsCount   int            // consecutive byte-identical single-tool calls
	lastSameArgsAt  time.Time      // when the last identical call's results came back
	correctionCount int            // nudges issued this turn before escalating to finalize

	// pendingCorrection holds a loop-correction nudge to inject on the next chat
	// call. Staged here (not appended directly) so it lands AFTER the tool result
	// in the transcript — never between an assistant tool_call and its result,
	// which providers reject.
	pendingCorrection string
}

// ownedGraphConfig is the dependency set for building the owned graph. It is
// assembled by NewAgent from the same inputs the eino path used, so the two
// loops are wired from identical components during the strangler period.
type ownedGraphConfig struct {
	model              model.ToolCallingChatModel
	tools              []tool.BaseTool
	toolInfos          []*schema.ToolInfo
	maxStep            int
	stepBudget         int           // finalize once this many tool rounds complete (0 = derive from maxStep)
	maxTurnDuration    time.Duration // finalize once the turn exceeds this wall-clock (0 = no time wall)
	messageModifier    func(ctx context.Context, input []*schema.Message) []*schema.Message
	messageRewriter    func(ctx context.Context, input []*schema.Message) []*schema.Message
	toolReturnDirectly map[string]struct{}
	streamToolChecker  func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error)

	// Tool-node behaviour carried over from the eino path for parity.
	unknownToolsHandler  func(ctx context.Context, name, input string) (string, error)     // hallucinated tool → graceful result
	toolArgumentsHandler func(ctx context.Context, name, arguments string) (string, error) // malformed args → sanitised
	executeSequentially  bool                                                              // run a step's tool calls sequentially

	// onTerminal is called when the graph diverts the turn into the finalize node
	// (budget wall or escalated loop). It records the reason for the outer
	// orchestration — used only for the hardcoded fallback if the finalize model
	// produces nothing. It receives the run context so it can reach the per-run
	// terminal holder. It must NOT abort the run: termination is the graph's job
	// via routing, not a context cancel. May be nil.
	onTerminal func(ctx context.Context, reason callbacks.TerminalReason, tool, detail string)

	// Loop-breaker thresholds (0 = use the defaults below).
	errLoopThreshold     int           // consecutive [ERROR] from one tool before correcting
	sameArgsThreshold    int           // byte-identical single-tool calls before correcting
	correctionBudget     int           // nudges allowed before escalating a loop to finalize
	identicalTightWindow time.Duration // identical calls within this window count as a tight loop; further apart = paced (reset)
}

// Loop-breaker defaults. identicalTightWindow encodes the false-positive guard:
// a legitimate poll (deploy status every few minutes, sleeping between) lands its
// identical calls far apart, so it never accrues a streak — only a back-to-back
// tight loop does.
const (
	defaultOwnedErrLoopThreshold     = 4
	defaultOwnedSameArgsThreshold    = 3
	defaultOwnedCorrectionBudget     = 2
	defaultOwnedIdenticalTightWindow = 5 * time.Second
)

func (c ownedGraphConfig) errLoopThresholdOr() int {
	if c.errLoopThreshold > 0 {
		return c.errLoopThreshold
	}
	return defaultOwnedErrLoopThreshold
}

func (c ownedGraphConfig) sameArgsThresholdOr() int {
	if c.sameArgsThreshold > 0 {
		return c.sameArgsThreshold
	}
	return defaultOwnedSameArgsThreshold
}

func (c ownedGraphConfig) correctionBudgetOr() int {
	if c.correctionBudget > 0 {
		return c.correctionBudget
	}
	return defaultOwnedCorrectionBudget
}

func (c ownedGraphConfig) identicalTightWindowOr() time.Duration {
	if c.identicalTightWindow > 0 {
		return c.identicalTightWindow
	}
	return defaultOwnedIdenticalTightWindow
}

// effectiveStepBudget is the tool-round count at which the loop must finalize.
// Derived from maxStep when not set explicitly. eino's own WithMaxRunSteps is a
// generous backstop set above this so OUR routing reaches finalize first.
func (c ownedGraphConfig) effectiveStepBudget() int {
	if c.stepBudget > 0 {
		return c.stepBudget
	}
	if c.maxStep > 0 {
		return c.maxStep
	}
	return 1 << 30 // effectively unlimited
}

// buildOwnedGraph constructs and compiles the ReAct graph. The result is a
// runnable taking the input messages and producing the final assistant message,
// identical in signature to eino's react.Agent so the call sites can swap.
func buildOwnedGraph(ctx context.Context, cfg ownedGraphConfig) (compose.Runnable[[]*schema.Message, *schema.Message], error) {
	// chat model bound to ALL tools; finalize model bound to NONE so it can only
	// emit text (feasibility point b: tools live on the model, not the node).
	chatModel, err := agent.ChatModelWithTools(nil, cfg.model, cfg.toolInfos)
	if err != nil {
		return nil, err
	}
	finalizeModel, err := agent.ChatModelWithTools(nil, cfg.model, nil)
	if err != nil {
		return nil, err
	}

	toolsNode, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:                cfg.tools,
		ExecuteSequentially:  cfg.executeSequentially,
		UnknownToolsHandler:  cfg.unknownToolsHandler,
		ToolArgumentsHandler: cfg.toolArgumentsHandler,
	})
	if err != nil {
		return nil, err
	}

	toolCallChecker := cfg.streamToolChecker
	if toolCallChecker == nil {
		toolCallChecker = ownedFirstChunkToolCallChecker
	}

	graph := compose.NewGraph[[]*schema.Message, *schema.Message](
		compose.WithGenLocalState(func(ctx context.Context) *ownedState {
			return &ownedState{
				Messages:  make([]*schema.Message, 0, cfg.maxStep+1),
				turnStart: time.Now(),
			}
		}),
	)

	// chat node: accumulate input into state, apply rewriter + modifier, call the
	// tool-bound model. Mirrors eino's modelPreHandle.
	chatPreHandle := func(ctx context.Context, input []*schema.Message, state *ownedState) ([]*schema.Message, error) {
		state.Messages = append(state.Messages, input...)
		// Inject any staged loop-correction nudge AFTER the tool result, so the
		// transcript stays well-formed (assistant tool_call → tool result → nudge).
		if state.pendingCorrection != "" {
			state.Messages = append(state.Messages, &schema.Message{
				Role:    schema.System,
				Content: state.pendingCorrection,
			})
			state.pendingCorrection = ""
		}
		if cfg.messageRewriter != nil {
			state.Messages = cfg.messageRewriter(ctx, state.Messages)
		}
		if cfg.messageModifier == nil {
			return state.Messages, nil
		}
		modified := make([]*schema.Message, len(state.Messages))
		copy(modified, state.Messages)
		return cfg.messageModifier(ctx, modified), nil
	}
	if err = graph.AddChatModelNode(ownedNodeChat, chatModel,
		compose.WithStatePreHandler(chatPreHandle), compose.WithNodeName("ChatModel")); err != nil {
		return nil, err
	}
	if err = graph.AddEdge(compose.START, ownedNodeChat); err != nil {
		return nil, err
	}

	// tools node: record the assistant message (content + tool_calls together),
	// capture any return-directly tool call.
	toolsPreHandle := func(ctx context.Context, input *schema.Message, state *ownedState) (*schema.Message, error) {
		if input == nil {
			return state.Messages[len(state.Messages)-1], nil
		}
		state.Messages = append(state.Messages, input)
		state.ReturnDirectlyToolCallID = ownedReturnDirectlyID(input, cfg.toolReturnDirectly)
		state.step++ // one tool round about to run — count it toward the step budget
		return input, nil
	}
	toolsPostHandle := func(ctx context.Context, output []*schema.Message, state *ownedState) ([]*schema.Message, error) {
		cfg.applyLoopPolicy(ctx, state, output)
		return output, nil
	}
	if err = graph.AddToolsNode(ownedNodeTools, toolsNode,
		compose.WithStatePreHandler(toolsPreHandle),
		compose.WithStatePostHandler(toolsPostHandle),
		compose.WithNodeName("Tools")); err != nil {
		return nil, err
	}

	// finalize node: tool-less model call fed the owned transcript + a finalize
	// directive, so the model writes its best summary from what it gathered.
	finalizePreHandle := func(ctx context.Context, input []*schema.Message, state *ownedState) ([]*schema.Message, error) {
		// On the tools→finalize path, input carries the latest tool results
		// (not yet in state); append them so the summary sees them. On the
		// chat→finalize path, input is empty (the unexecuted tool-call message is
		// deliberately dropped). Then feed the transcript + finalize directive.
		state.Messages = append(state.Messages, input...)
		msgs := make([]*schema.Message, len(state.Messages))
		copy(msgs, state.Messages)
		return appendFinalizeDirective(msgs), nil
	}
	if err = graph.AddChatModelNode(ownedNodeFinalize, finalizeModel,
		compose.WithStatePreHandler(finalizePreHandle), compose.WithNodeName("Finalize")); err != nil {
		return nil, err
	}
	if err = graph.AddEdge(ownedNodeFinalize, compose.END); err != nil {
		return nil, err
	}

	// chat→finalize type adapter: the chat node outputs a single *schema.Message
	// but the finalize ChatModel node consumes []*schema.Message. The unexecuted
	// tool-call message is dropped (it has no matching tool result), so the
	// adapter emits an empty slice and finalize reads the clean transcript from
	// state.
	chatToFinalize := func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (*schema.StreamReader[[]*schema.Message], error) {
		sr.Close()
		out, w := schema.Pipe[[]*schema.Message](1)
		w.Send([]*schema.Message{}, nil)
		w.Close()
		return out, nil
	}
	if err = graph.AddLambdaNode(ownedNodeChatToFinalize, compose.TransformableLambda(chatToFinalize)); err != nil {
		return nil, err
	}
	if err = graph.AddEdge(ownedNodeChatToFinalize, ownedNodeFinalize); err != nil {
		return nil, err
	}

	// chat → {tools | finalize | END}: tool calls continue to tools unless a
	// budget wall has been reached, in which case go straight to finalize; no
	// tool calls means the model answered, so END.
	chatBranch := func(ctx context.Context, sr *schema.StreamReader[*schema.Message]) (string, error) {
		isToolCall, err := toolCallChecker(ctx, sr)
		if err != nil {
			return "", err
		}
		if !isToolCall {
			return compose.END, nil
		}
		// The model wants to call a tool. If the turn has hit a budget wall,
		// record the reason and divert to finalize instead of running the tool —
		// the model gets one tool-less call to summarise what it already gathered.
		var wallReason callbacks.TerminalReason
		if perr := compose.ProcessState(ctx, func(_ context.Context, s *ownedState) error {
			if reason := cfg.budgetWallReason(s); reason != callbacks.TerminalNone {
				if s.terminalReason == callbacks.TerminalNone {
					s.terminalReason = reason
				}
				wallReason = s.terminalReason
			}
			return nil
		}); perr != nil {
			return "", perr
		}
		if wallReason != callbacks.TerminalNone {
			if cfg.onTerminal != nil {
				cfg.onTerminal(ctx, wallReason, "", "")
			}
			return ownedNodeChatToFinalize, nil
		}
		return ownedNodeTools, nil
	}
	if err = graph.AddBranch(ownedNodeChat, compose.NewStreamGraphBranch(chatBranch,
		map[string]bool{ownedNodeTools: true, ownedNodeChatToFinalize: true, compose.END: true})); err != nil {
		return nil, err
	}

	if err = buildOwnedReturnDirectly(graph); err != nil {
		return nil, err
	}

	return graph.Compile(ctx,
		compose.WithMaxRunSteps(cfg.maxStep),
		compose.WithNodeTriggerMode(compose.AnyPredecessor),
		compose.WithGraphName("OwnedReActAgent"),
	)
}

// buildOwnedReturnDirectly wires the tools post-branch: HITL/return-directly →
// direct_return → END; otherwise back to chat (or finalize once loop escalation
// lands in step 4). Mirrors eino's buildReturnDirectly.
func buildOwnedReturnDirectly(graph *compose.Graph[[]*schema.Message, *schema.Message]) error {
	directReturn := func(ctx context.Context, msgs *schema.StreamReader[[]*schema.Message]) (*schema.StreamReader[*schema.Message], error) {
		return schema.StreamReaderWithConvert(msgs, func(msgs []*schema.Message) (*schema.Message, error) {
			var picked *schema.Message
			err := compose.ProcessState(ctx, func(_ context.Context, state *ownedState) error {
				for i := range msgs {
					if msgs[i] != nil && msgs[i].ToolCallID == state.ReturnDirectlyToolCallID {
						picked = msgs[i]
						return nil
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if picked == nil {
				return nil, schema.ErrNoValue
			}
			return picked, nil
		}), nil
	}
	if err := graph.AddLambdaNode(ownedNodeDirectReturn, compose.TransformableLambda(directReturn)); err != nil {
		return err
	}

	toolsBranch := func(ctx context.Context, msgsStream *schema.StreamReader[[]*schema.Message]) (string, error) {
		msgsStream.Close()
		endNode := ownedNodeChat
		err := compose.ProcessState(ctx, func(_ context.Context, state *ownedState) error {
			if len(state.ReturnDirectlyToolCallID) > 0 {
				endNode = ownedNodeDirectReturn
				return nil
			}
			if state.terminalReason != callbacks.TerminalNone {
				endNode = ownedNodeFinalize
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		return endNode, nil
	}
	if err := graph.AddBranch(ownedNodeTools, compose.NewStreamGraphBranch(toolsBranch,
		map[string]bool{ownedNodeChat: true, ownedNodeDirectReturn: true, ownedNodeFinalize: true})); err != nil {
		return err
	}

	return graph.AddEdge(ownedNodeDirectReturn, compose.END)
}

// appendFinalizeDirective appends a system message carrying the finalize
// directive so the tool-less model call is told, in-band, to answer now from the
// gathered context. Reuses the same directive text the soft-landing path injects.
func appendFinalizeDirective(msgs []*schema.Message) []*schema.Message {
	return append(msgs, &schema.Message{
		Role:    schema.System,
		Content: finalizeDirective,
	})
}

// ownedReturnDirectlyID returns the call ID of the first return-directly tool in
// the assistant message, or "".
func ownedReturnDirectlyID(input *schema.Message, returnDirectly map[string]struct{}) string {
	if len(returnDirectly) == 0 {
		return ""
	}
	for _, tc := range input.ToolCalls {
		if _, ok := returnDirectly[tc.Function.Name]; ok {
			return tc.ID
		}
	}
	return ""
}

// budgetWallReason reports the terminal reason if the turn has reached a budget
// wall and must finalize instead of calling another tool, or TerminalNone if it
// may continue. An already-recorded reason (a loop escalation set elsewhere)
// wins so the root cause is preserved.
func (c ownedGraphConfig) budgetWallReason(s *ownedState) callbacks.TerminalReason {
	if s.terminalReason != callbacks.TerminalNone {
		return s.terminalReason
	}
	if c.maxTurnDuration > 0 && time.Since(s.turnStart) >= c.maxTurnDuration {
		return callbacks.TerminalTimeBudget
	}
	if s.step >= c.effectiveStepBudget() {
		return callbacks.TerminalStepBudget
	}
	return callbacks.TerminalNone
}

// ownedFirstChunkToolCallChecker is the default streaming tool-call detector:
// the model is calling a tool iff the first non-empty chunk carries tool calls.
// Equivalent to eino's unexported firstChunkStreamToolCallChecker.
func ownedFirstChunkToolCallChecker(_ context.Context, sr *schema.StreamReader[*schema.Message]) (bool, error) {
	defer sr.Close()
	for {
		msg, err := sr.Recv()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if len(msg.ToolCalls) > 0 {
			return true, nil
		}
		if len(msg.Content) == 0 {
			continue
		}
		return false, nil
	}
}
