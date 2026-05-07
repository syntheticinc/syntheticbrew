/** Callback events emitted during streaming */
export interface ChatCallbacks {
  onDelta: (content: string) => void;
  onToolCallStart: (tool: string, input: string) => void;
  onToolCallResult: (tool: string, result: string) => void;
  onDone: (sessionId: string) => void;
  onError: (error: string) => void;
}

export interface ChatConfig {
  schemaName: string;
  endpoint: string;
  apiKey: string | null;
}

const SESSION_KEY_PREFIX = 'bb_widget_session_';

export class ChatClient {
  private config: ChatConfig;
  private sessionId: string | null;
  private abortController: AbortController | null = null;

  constructor(config: ChatConfig) {
    this.config = config;
    this.sessionId = this.loadSessionId();
  }

  private storageKey(): string {
    return SESSION_KEY_PREFIX + this.config.schemaName;
  }

  private loadSessionId(): string | null {
    try {
      return localStorage.getItem(this.storageKey());
    } catch {
      return null;
    }
  }

  private saveSessionId(id: string): void {
    this.sessionId = id;
    try {
      localStorage.setItem(this.storageKey(), id);
    } catch {
      // localStorage unavailable — session will not persist
    }
  }

  abort(): void {
    if (this.abortController) {
      this.abortController.abort();
      this.abortController = null;
    }
  }

  async send(message: string, callbacks: ChatCallbacks): Promise<void> {
    this.abort();
    this.abortController = new AbortController();

    const url = `${this.config.endpoint}/api/v1/schemas/${encodeURIComponent(this.config.schemaName)}/chat`;

    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
    };
    if (this.config.apiKey) {
      headers['Authorization'] = `Bearer ${this.config.apiKey}`;
    }

    const body: Record<string, string> = { message };
    if (this.sessionId) {
      body['session_id'] = this.sessionId;
    }

    let response: Response;
    try {
      response = await fetch(url, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal: this.abortController.signal,
      });
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      callbacks.onError(`Connection failed: ${String(err)}`);
      return;
    }

    if (!response.ok) {
      const text = await response.text().catch(() => 'Unknown error');
      callbacks.onError(`Server error ${response.status}: ${text}`);
      return;
    }

    if (!response.body) {
      callbacks.onError('No response body');
      return;
    }

    await this.readSSE(response.body, callbacks);
  }

  private async readSSE(body: ReadableStream<Uint8Array>, callbacks: ChatCallbacks): Promise<void> {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let currentEvent = '';

    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';

        for (const line of lines) {
          if (line.startsWith('event: ')) {
            currentEvent = line.slice(7).trim();
            continue;
          }

          if (line.startsWith('data: ')) {
            const dataStr = line.slice(6);
            this.handleEvent(currentEvent, dataStr, callbacks);
            currentEvent = '';
            continue;
          }

          // Empty line resets event type per SSE spec
          if (line.trim() === '') {
            currentEvent = '';
          }
        }
      }
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      callbacks.onError(`Stream error: ${String(err)}`);
    } finally {
      reader.releaseLock();
    }
  }

  private handleEvent(event: string, dataStr: string, callbacks: ChatCallbacks): void {
    let data: Record<string, unknown>;
    try {
      data = JSON.parse(dataStr);
    } catch {
      // Non-JSON data line — skip
      return;
    }

    switch (event) {
      case 'message_delta':
        if (typeof data.content === 'string') {
          callbacks.onDelta(data.content);
        }
        break;

      case 'tool_call_start':
        callbacks.onToolCallStart(
          String(data.tool ?? 'tool'),
          typeof data.input === 'string' ? data.input : JSON.stringify(data.input ?? ''),
        );
        break;

      case 'tool_call_result':
        callbacks.onToolCallResult(
          String(data.tool ?? 'tool'),
          typeof data.result === 'string' ? data.result : JSON.stringify(data.result ?? ''),
        );
        break;

      case 'done':
        if (typeof data.session_id === 'string') {
          this.saveSessionId(data.session_id);
        }
        callbacks.onDone(String(data.session_id ?? ''));
        break;

      case 'error':
        callbacks.onError(String(data.message ?? data.error ?? 'Unknown server error'));
        break;
    }
  }
}
