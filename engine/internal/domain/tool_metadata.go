package domain

// ToolExtraReturnDirectly is the key under schema.ToolInfo.Extra used to carry a
// tool's self-declared "return-directly" intent from the tool source (e.g. the
// MCP adapter) to the ReAct loop. When true, the loop ends right after the tool
// runs and surfaces the tool result as the answer, with no follow-up model call.
//
// This is an internal engine contract (the value of the Extra map key), distinct
// from any wire-level declaration an external tool uses to set it.
const ToolExtraReturnDirectly = "return_directly"
