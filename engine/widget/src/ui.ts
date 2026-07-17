import {
  ChatClient,
  type ChatCallbacks,
  type InterruptAnswer,
  type InterruptRequestPayload,
  type InterruptResumePayload,
  type InterruptSchema,
} from './chat';
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
  private interruptWidgets = new Map<string, HTMLElement>();
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
    this.host.id = 'syntheticbrew-widget';
    this.shadow = this.host.attachShadow({ mode: 'closed' });

    // Inject styles
    const style = document.createElement('style');
    const theme = getTheme(config.theme, config.primaryColor);
    style.textContent = buildStyles(theme, config.position);
    this.shadow.appendChild(style);

    // Build UI
    this.buildBubble();
    this.buildPanel();
    void this.applyAttribution();

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

  /** Render the "Powered by SyntheticBrew" badge in the header when the
   *  operator's widget config enables attribution. fetchWidgetConfig is
   *  fail-quiet ({ attribution: false } on any failure), so a missing or
   *  broken config endpoint leaves the header untouched. */
  private async applyAttribution(): Promise<void> {
    const { attribution } = await this.client.fetchWidgetConfig();
    if (!attribution) return;

    const title = this.panel.querySelector('.bb-header-title');
    if (!title) return;

    const badge = document.createElement('a');
    badge.className = 'bb-attribution';
    badge.href = 'https://syntheticbrew.ai?utm_source=widget';
    badge.target = '_blank';
    badge.rel = 'nofollow noopener noreferrer';
    badge.textContent = 'Powered by SyntheticBrew';
    title.insertAdjacentElement('afterend', badge);
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

      onInterruptRequest: (payload) => {
        if (!assistantStarted) {
          typingEl.remove();
          assistantStarted = true;
        }
        this.addInterruptWidget(payload, callbacks);
      },

      onInterruptResume: (payload) => {
        // User's just-submitted resume is echoed back; mark the matching
        // widget answered. Do NOT add a chat bubble — the widget's selected
        // state is the user-visible record.
        this.markInterruptAnswered(payload);
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

  // ─── HITL Interrupt Primitive — DOM rendering ─────────────────────────────

  /** Render a HITL widget from the engine's interrupt_request schema. Stores
   *  the rendered container by interrupt_id so the matching interrupt_resume
   *  can flip controls into disabled / answered state. */
  private addInterruptWidget(payload: InterruptRequestPayload, callbacks: ChatCallbacks): void {
    const container = document.createElement('div');
    container.className = 'bb-msg bb-msg-assistant bb-interrupt';
    container.dataset.interruptId = payload.interrupt_id;
    container.dataset.state = 'pending';
    this.renderInterruptBody(container, payload.schema, payload.interrupt_id, callbacks, 'pending', []);
    this.messagesEl.appendChild(container);
    this.interruptWidgets.set(payload.interrupt_id, container);
    this.scrollToBottom();
  }

  /** Replace the widget's body with an answered view — selected option
   *  highlighted, controls disabled. Idempotent: called once per resume. */
  private markInterruptAnswered(payload: InterruptResumePayload): void {
    const container = this.interruptWidgets.get(payload.interrupt_id);
    if (!container) return;
    const schema = this.recoverSchemaFromDOM(container);
    if (!schema) return;
    container.dataset.state = 'answered';
    container.innerHTML = '';
    // No-op callbacks — controls won't fire submits any more.
    const noopCallbacks: ChatCallbacks = {
      onDelta: () => {},
      onToolCallStart: () => {},
      onToolCallResult: () => {},
      onInterruptRequest: () => {},
      onInterruptResume: () => {},
      onDone: () => {},
      onError: () => {},
    };
    this.renderInterruptBody(container, schema, payload.interrupt_id, noopCallbacks, 'answered', payload.payload.answers);
  }

  /** Schema is stashed onto the container as a JSON-encoded data attribute so
   *  marking-answered can rebuild the answered view without holding the full
   *  payload in a separate map. */
  private recoverSchemaFromDOM(container: HTMLElement): InterruptSchema | null {
    const raw = container.dataset.schema;
    if (!raw) return null;
    try {
      return JSON.parse(raw) as InterruptSchema;
    } catch {
      return null;
    }
  }

  private renderInterruptBody(
    container: HTMLElement,
    schema: InterruptSchema,
    interruptId: string,
    callbacks: ChatCallbacks,
    state: 'pending' | 'answered',
    answers: InterruptAnswer[],
  ): void {
    container.dataset.schema = JSON.stringify(schema);
    const disabled = state === 'answered';

    if (schema.title) {
      const titleEl = document.createElement('div');
      titleEl.className = 'bb-interrupt-title';
      titleEl.textContent = schema.title;
      container.appendChild(titleEl);
    }
    if (schema.description) {
      const descEl = document.createElement('div');
      descEl.className = 'bb-interrupt-desc';
      descEl.textContent = schema.description;
      container.appendChild(descEl);
    }

    if (schema.output_type === 'summary_table') {
      if (schema.rows && schema.rows.length > 0) {
        const tableEl = document.createElement('div');
        tableEl.className = 'bb-interrupt-table';
        for (const row of schema.rows) {
          const rowEl = document.createElement('div');
          rowEl.className = 'bb-interrupt-row';
          const label = document.createElement('span');
          label.className = 'bb-interrupt-row-label';
          label.textContent = row.label;
          const value = document.createElement('span');
          value.className = 'bb-interrupt-row-value';
          value.textContent = row.value;
          rowEl.append(label, value);
          tableEl.appendChild(rowEl);
        }
        container.appendChild(tableEl);
      }
      if (schema.actions && schema.actions.length > 0) {
        const actionsEl = document.createElement('div');
        actionsEl.className = 'bb-interrupt-actions';
        const selectedValue = answers[0]?.value;
        for (const action of schema.actions) {
          const btn = document.createElement('button');
          btn.type = 'button';
          btn.className = `bb-interrupt-btn bb-interrupt-btn-${action.type}`;
          btn.textContent = action.label;
          btn.disabled = disabled;
          if (disabled && selectedValue === action.value) {
            btn.classList.add('bb-interrupt-btn-selected');
          }
          btn.addEventListener('click', () => {
            void this.client.sendInterruptResume(
              interruptId,
              [{ question_id: 'action', value: action.value, label: action.label }],
              callbacks,
            );
          });
          actionsEl.appendChild(btn);
        }
        container.appendChild(actionsEl);
      }
      return;
    }

    if (schema.output_type === 'form') {
      const formEl = document.createElement('div');
      formEl.className = 'bb-interrupt-form';
      const inputs = new Map<string, () => InterruptAnswer>();

      for (const q of schema.questions ?? []) {
        const fieldEl = document.createElement('div');
        fieldEl.className = 'bb-interrupt-field';
        const labelEl = document.createElement('label');
        labelEl.className = 'bb-interrupt-field-label';
        labelEl.textContent = q.label;
        fieldEl.appendChild(labelEl);

        const existing = answers.find((a) => a.question_id === q.id);

        if (q.type === 'text') {
          const input = document.createElement('input');
          input.type = 'text';
          input.className = 'bb-interrupt-input';
          input.value = existing?.value ?? q.default ?? '';
          input.disabled = disabled;
          fieldEl.appendChild(input);
          inputs.set(q.id, () => ({ question_id: q.id, value: input.value }));
        } else {
          // select / multiselect — render as clickable chips
          let current = existing?.value ?? q.default ?? '';
          const multi = q.type === 'multiselect';
          const selected = new Set<string>(multi && current ? current.split('\n') : current ? [current] : []);
          const chips = document.createElement('div');
          chips.className = 'bb-interrupt-chips';
          for (const opt of q.options ?? []) {
            const optVal = opt.value ?? opt.label;
            const chip = document.createElement('button');
            chip.type = 'button';
            chip.className = 'bb-interrupt-chip';
            chip.textContent = opt.label;
            chip.disabled = disabled;
            if (selected.has(optVal)) chip.classList.add('bb-interrupt-chip-selected');
            chip.addEventListener('click', () => {
              if (disabled) return;
              if (multi) {
                if (selected.has(optVal)) selected.delete(optVal);
                else selected.add(optVal);
              } else {
                selected.clear();
                selected.add(optVal);
                for (const sibling of chips.querySelectorAll<HTMLButtonElement>('.bb-interrupt-chip')) {
                  sibling.classList.remove('bb-interrupt-chip-selected');
                }
              }
              if (selected.has(optVal)) chip.classList.add('bb-interrupt-chip-selected');
              else chip.classList.remove('bb-interrupt-chip-selected');
              current = multi ? Array.from(selected).join('\n') : optVal;
            });
            chips.appendChild(chip);
          }
          fieldEl.appendChild(chips);
          inputs.set(q.id, () => {
            const label = (q.options ?? []).find((o) => (o.value ?? o.label) === current)?.label;
            return { question_id: q.id, value: current, label };
          });
        }
        formEl.appendChild(fieldEl);
      }

      const submitBtn = document.createElement('button');
      submitBtn.type = 'button';
      submitBtn.className = 'bb-interrupt-btn bb-interrupt-btn-primary';
      submitBtn.textContent = disabled ? 'Submitted' : 'Submit';
      submitBtn.disabled = disabled;
      submitBtn.addEventListener('click', () => {
        const collected: InterruptAnswer[] = [];
        for (const get of inputs.values()) collected.push(get());
        void this.client.sendInterruptResume(interruptId, collected, callbacks);
      });
      formEl.appendChild(submitBtn);

      container.appendChild(formEl);
    }
    // output_type === 'info' renders title + description only (no controls).
  }
}
