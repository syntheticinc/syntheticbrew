import { ChatClient, type ChatCallbacks } from './chat';
import { renderMarkdown } from './markdown';
import { buildStyles, getTheme } from './styles';

export interface WidgetConfig {
  schemaName: string;
  apiKey: string | null;
  endpoint: string;
  position: string;
  theme: string;
  title: string;
  primaryColor: string | null;
  welcomeMessage: string | null;
  placeholderText: string | null;
}

// SVG icons
const CHAT_ICON = `<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M20 2H4c-1.1 0-2 .9-2 2v18l4-4h14c1.1 0 2-.9 2-2V4c0-1.1-.9-2-2-2zm0 14H5.17L4 17.17V4h16v12z"/><path d="M7 9h10v2H7zm0-3h10v2H7zm0 6h7v2H7z"/></svg>`;
const CLOSE_ICON = `<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z" fill="currentColor"/></svg>`;
const SEND_ICON = `<svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg"><path d="M2.01 21L23 12 2.01 3 2 10l15 2-15 2z" fill="currentColor"/></svg>`;

export class WidgetUI {
  private host: HTMLElement;
  private shadow: ShadowRoot;
  private client: ChatClient;
  private config: WidgetConfig;

  // DOM refs
  private panel!: HTMLElement;
  private messagesEl!: HTMLElement;
  private input!: HTMLTextAreaElement;
  private sendBtn!: HTMLButtonElement;
  private bubble!: HTMLButtonElement;

  // State
  private isOpen = false;
  private isStreaming = false;
  private currentAssistantEl: HTMLElement | null = null;
  private currentAssistantContent = '';

  constructor(config: WidgetConfig) {
    this.config = config;
    this.client = new ChatClient({
      schemaName: config.schemaName,
      endpoint: config.endpoint,
      apiKey: config.apiKey,
    });

    // Create host element
    this.host = document.createElement('div');
    this.host.id = 'bytebrew-widget';
    this.shadow = this.host.attachShadow({ mode: 'closed' });

    // Inject styles
    const style = document.createElement('style');
    const theme = getTheme(config.theme, config.primaryColor);
    style.textContent = buildStyles(theme, config.position);
    this.shadow.appendChild(style);

    // Build UI
    this.buildBubble();
    this.buildPanel();

    // Show welcome message if configured
    if (config.welcomeMessage) {
      this.addAssistantMessage(config.welcomeMessage);
    }

    // Mount
    document.body.appendChild(this.host);
  }

  private buildBubble(): void {
    this.bubble = document.createElement('button');
    this.bubble.className = 'bb-bubble';
    this.bubble.setAttribute('aria-label', 'Open chat');
    this.bubble.innerHTML = CHAT_ICON;
    this.bubble.addEventListener('click', () => this.toggle());
    this.shadow.appendChild(this.bubble);
  }

  private buildPanel(): void {
    this.panel = document.createElement('div');
    this.panel.className = 'bb-panel';
    this.panel.setAttribute('role', 'dialog');
    this.panel.setAttribute('aria-label', this.config.title);
    this.panel.setAttribute('aria-hidden', 'true');

    // Header
    const header = document.createElement('div');
    header.className = 'bb-header';

    const title = document.createElement('span');
    title.className = 'bb-header-title';
    title.textContent = this.config.title;

    const closeBtn = document.createElement('button');
    closeBtn.className = 'bb-close-btn';
    closeBtn.setAttribute('aria-label', 'Close chat');
    closeBtn.innerHTML = CLOSE_ICON;
    closeBtn.addEventListener('click', () => this.toggle());

    header.appendChild(title);
    header.appendChild(closeBtn);
    this.panel.appendChild(header);

    // Messages
    this.messagesEl = document.createElement('div');
    this.messagesEl.className = 'bb-messages';
    this.messagesEl.setAttribute('role', 'log');
    this.messagesEl.setAttribute('aria-live', 'polite');
    this.panel.appendChild(this.messagesEl);

    // Input area
    const inputArea = document.createElement('div');
    inputArea.className = 'bb-input-area';

    this.input = document.createElement('textarea');
    this.input.className = 'bb-input';
    this.input.placeholder = this.config.placeholderText ?? 'Type a message...';
    this.input.rows = 1;
    this.input.setAttribute('aria-label', 'Message input');
    this.input.addEventListener('keydown', (e) => this.handleKeydown(e));
    this.input.addEventListener('input', () => this.autoResize());

    this.sendBtn = document.createElement('button');
    this.sendBtn.className = 'bb-send-btn';
    this.sendBtn.setAttribute('aria-label', 'Send message');
    this.sendBtn.innerHTML = SEND_ICON;
    this.sendBtn.addEventListener('click', () => this.sendMessage());

    inputArea.appendChild(this.input);
    inputArea.appendChild(this.sendBtn);
    this.panel.appendChild(inputArea);

    this.shadow.appendChild(this.panel);
  }

  private toggle(): void {
    this.isOpen = !this.isOpen;
    this.panel.setAttribute('aria-hidden', String(!this.isOpen));
    this.bubble.innerHTML = this.isOpen ? CLOSE_ICON : CHAT_ICON;
    this.bubble.setAttribute('aria-label', this.isOpen ? 'Close chat' : 'Open chat');

    if (this.isOpen) {
      requestAnimationFrame(() => this.input.focus());
    }
  }

  private handleKeydown(e: KeyboardEvent): void {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      this.sendMessage();
    }
  }

  private autoResize(): void {
    this.input.style.height = 'auto';
    this.input.style.height = Math.min(this.input.scrollHeight, 100) + 'px';
  }

  private scrollToBottom(): void {
    requestAnimationFrame(() => {
      this.messagesEl.scrollTop = this.messagesEl.scrollHeight;
    });
  }

  private addUserMessage(text: string): void {
    const el = document.createElement('div');
    el.className = 'bb-msg bb-msg-user';
    el.textContent = text;
    this.messagesEl.appendChild(el);
    this.scrollToBottom();
  }

  private addAssistantMessage(text: string): void {
    const el = document.createElement('div');
    el.className = 'bb-msg bb-msg-assistant';
    el.setAttribute('role', 'article');
    el.innerHTML = renderMarkdown(text);
    this.messagesEl.appendChild(el);
  }

  private startAssistantMessage(): HTMLElement {
    const el = document.createElement('div');
    el.className = 'bb-msg bb-msg-assistant';
    el.setAttribute('role', 'article');
    this.messagesEl.appendChild(el);
    this.currentAssistantEl = el;
    this.currentAssistantContent = '';
    this.scrollToBottom();
    return el;
  }

  private appendToAssistant(content: string): void {
    this.currentAssistantContent += content;
    if (this.currentAssistantEl) {
      this.currentAssistantEl.innerHTML = renderMarkdown(this.currentAssistantContent);
      this.scrollToBottom();
    }
  }

  private addTypingIndicator(): HTMLElement {
    const el = document.createElement('div');
    el.className = 'bb-typing';
    el.setAttribute('aria-label', 'Assistant is typing');
    for (let i = 0; i < 3; i++) {
      const dot = document.createElement('span');
      dot.className = 'bb-typing-dot';
      el.appendChild(dot);
    }
    this.messagesEl.appendChild(el);
    this.scrollToBottom();
    return el;
  }

  private addToolCall(toolName: string): { header: HTMLElement; body: HTMLElement; el: HTMLElement } {
    const el = document.createElement('div');
    el.className = 'bb-tool';

    const header = document.createElement('div');
    header.className = 'bb-tool-header';
    header.innerHTML = `<span class="bb-tool-spinner"></span><span>Using tool: ${this.escapeText(toolName)}</span>`;
    header.addEventListener('click', () => {
      body.classList.toggle('bb-expanded');
    });

    const body = document.createElement('div');
    body.className = 'bb-tool-body';

    el.appendChild(header);
    el.appendChild(body);
    this.messagesEl.appendChild(el);
    this.scrollToBottom();

    return { header, body, el };
  }

  private escapeText(text: string): string {
    const div = document.createElement('span');
    div.textContent = text;
    return div.innerHTML;
  }

  private setStreaming(streaming: boolean): void {
    this.isStreaming = streaming;
    this.sendBtn.disabled = streaming;
    this.input.disabled = streaming;
  }

  private async sendMessage(): Promise<void> {
    const text = this.input.value.trim();
    if (!text || this.isStreaming) return;

    this.input.value = '';
    this.input.style.height = 'auto';
    this.addUserMessage(text);
    this.setStreaming(true);

    const typingEl = this.addTypingIndicator();
    let assistantStarted = false;
    const activeTools = new Map<string, { header: HTMLElement; body: HTMLElement; el: HTMLElement }>();

    const callbacks: ChatCallbacks = {
      onDelta: (content) => {
        // Remove typing indicator on first delta
        if (!assistantStarted) {
          typingEl.remove();
          this.startAssistantMessage();
          assistantStarted = true;
        }
        this.appendToAssistant(content);
      },

      onToolCallStart: (tool) => {
        if (!assistantStarted) {
          typingEl.remove();
          assistantStarted = true;
        }
        const toolEl = this.addToolCall(tool);
        activeTools.set(tool, toolEl);
      },

      onToolCallResult: (tool, result) => {
        const toolEl = activeTools.get(tool);
        if (toolEl) {
          // Replace spinner with checkmark
          const spinner = toolEl.header.querySelector('.bb-tool-spinner');
          if (spinner) {
            spinner.outerHTML = '<span class="bb-tool-check">&#10003;</span>';
          }
          toolEl.body.textContent = result;
          activeTools.delete(tool);
        }
      },

      onDone: () => {
        typingEl.remove();
        this.setStreaming(false);
        this.currentAssistantEl = null;
        this.input.focus();
      },

      onError: (error) => {
        typingEl.remove();
        if (!assistantStarted) {
          const errEl = document.createElement('div');
          errEl.className = 'bb-msg bb-msg-assistant';
          errEl.style.color = '#ef4444';
          errEl.textContent = error;
          this.messagesEl.appendChild(errEl);
        }
        this.setStreaming(false);
        this.currentAssistantEl = null;
        this.input.focus();
        this.scrollToBottom();
      },
    };

    await this.client.send(text, callbacks);

    // Ensure streaming state is reset even if no done event
    if (this.isStreaming) {
      this.setStreaming(false);
      this.currentAssistantEl = null;
    }
  }
}
