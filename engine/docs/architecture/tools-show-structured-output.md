# `show_structured_output` tool — integrator reference

The `show_structured_output` tool emits a structured widget to the chat client
and halts the React loop. The user's reply (for `form` mode) arrives as a
typed HITL resume on the next turn — see the HITL Interrupt Primitive
(engine 1.2.0+) for the wire format.

This page documents the strict input contract enforced from engine 1.2.2
onwards, the supported shapes, and a recommended prompt pattern for agents
that use this tool.

## Strict input contract

Invalid arguments return `[ERROR] …` strings to the agent rather than
silently emitting a degenerate widget:

- **Unknown top-level fields** → `[ERROR] json: unknown field "X"`. The
  decoder uses `DisallowUnknownFields` at every level (top-level args,
  individual `questions[]` entries, individual `options[]` entries).
- **Unknown `output_type` values** → `[ERROR] unknown output_type "X".
  Supported: summary_table | form | info`. The closed set is enforced
  before any other validation runs.
- **Malformed nested arrays** → `[ERROR] … failed to parse …`. Stringified
  arrays at any depth are still accepted as a lenient fallback (sub-frontier
  LLMs frequently emit them), but malformed strings fail loud instead of
  being silently dropped.

## Supported shapes

### `output_type: "summary_table"`

Displays labelled rows plus optional action buttons. Submitting an action
button is equivalent to answering a single-question form with that
button's `value`.

```json
{
  "output_type": "summary_table",
  "title": "Confirm deletion",
  "description": "This cannot be undone.",
  "rows": [
    {"label": "target", "value": "prod-db"}
  ],
  "actions": [
    {"label": "Delete", "type": "primary",   "value": "delete"},
    {"label": "Cancel", "type": "secondary", "value": "cancel"}
  ]
}
```

### `output_type: "form"`

Collects 1–10 inputs from the user. Each question has a type
(`text` | `select` | `multiselect`). `select` and `multiselect` require 2–5
options.

```json
{
  "output_type": "form",
  "title": "Configure device",
  "questions": [
    {
      "id": "name",
      "label": "Device name",
      "type": "text"
    },
    {
      "id": "platform",
      "label": "Platform?",
      "type": "select",
      "options": [
        {"label": "iOS"},
        {"label": "Android", "value": "android"}
      ]
    }
  ]
}
```

For **single-question forms** the `id` field may be omitted — the engine
generates a synthetic `id` derived from the server-issued `interrupt_id`
(format `q-<8 hex chars>`). The synthetic id appears in the resume payload
as `answers[0].question_id` and is opaque; integrators should correlate
answers by `interrupt_id`, not by question id.

For **multi-question forms** every question must declare an `id` — these
are how the resume payload maps answers back to questions.

### `output_type: "info"`

Title + description only, no inputs collected. The agent's turn still halts;
the next user message resumes the loop.

```json
{
  "output_type": "info",
  "title": "Deployment status",
  "description": "Rolling restart in progress."
}
```

## Lenient parsing of stringified arrays

Sub-frontier LLMs frequently emit array fields as JSON-string literals
(`"rows": "[{...}]"` instead of `"rows": [{...}]`). The tool's JSON Schema
declares `rows`, `actions`, and `questions` as strings for exactly this
reason — the engine accepts both shapes and parses them identically.

This lenient behaviour applies at every depth:

```json
{
  "output_type": "form",
  "questions": [
    {
      "id": "region",
      "label": "Region?",
      "type": "select",
      "options": "[{\"label\":\"EU\"},{\"label\":\"US\"}]"
    }
  ]
}
```

A stringified value that fails to parse is surfaced as a tool error rather
than treated as empty.

## Recommended prompt pattern

The engine surfacing `[ERROR] …` is necessary but not sufficient. The
agent's prompt must also instruct the model on what to do when it sees the
error. Without that instruction smaller models routinely retry the same
invalid args and burn through `max_steps`. Add a block similar to the
following to the agent's system prompt:

```text
TOOL ERROR HANDLING

If a tool returns a string starting with "[ERROR]", do NOT retry with the
same arguments. Read the error message carefully and either:
  1. Adjust the arguments to match the contract described in the tool's
     description, OR
  2. Escalate the problem to the user with a brief explanation if you
     cannot determine the correct arguments.

For show_structured_output specifically: re-read the tool description's
"STRICT INPUT CONTRACT" section before deciding how to retry. Field names,
the set of allowed output_type values, and nested array shapes are
strict — sending an "almost right" payload returns the same error.
```

## Known model-behaviour pitfalls

Frontier-tier models (Claude, GPT-4o, o1) tend to match the contract on
the first emit. Sub-frontier and OSS-hosted models routinely hit one of
these failure modes:

| Symptom | Cause | Engine 1.2.2 behaviour |
|---|---|---|
| Widget renders with no question / no rows / no actions | Model invented a top-level shape like `{"output_type":"single_select","prompt":"…","options":"[…]"}` | `[ERROR] unknown output_type "single_select"` — agent must adjust |
| Widget renders but options are empty | Model emitted `"options": "[…]"` as JSON-string at any depth | Lenient parser at every level handles this transparently |
| Agent stalls after a `show_structured_output` call with an `[ERROR]` reply | Prompt missing the "STOP on tool error" rule | Add the recommended pattern above |
| `[ERROR] json: unknown field "X"` | Model added a field not in the contract (`prompt`, `single_select`, etc.) | Adjust args or rebase the system prompt against the current contract |

## Limits

- `maxQuestions = 10` (raised from 5 in engine 1.2.2). Per-tenant or
  per-agent configurability is on the 1.3.0 roadmap.
- `maxQuestionOpts = 5` per `select`/`multiselect` question.
- `questions[].type` is one of `text` | `select` | `multiselect`. A
  first-class `confirm` question type is on the 1.3.0 roadmap.
