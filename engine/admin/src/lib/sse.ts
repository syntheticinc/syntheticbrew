// Shared SSE (Server-Sent Events) parsing utilities for SyntheticBrew admin components.

export interface ToolCall {
  tool: string;
  input?: string;
  output?: string;
}

export function parseSSELine(line: string): { event?: string; data?: string } {
  if (line.startsWith('event: ')) return { event: line.slice(7).trim() };
  if (line.startsWith('data: ')) return { data: line.slice(6) };
  return {};
}
