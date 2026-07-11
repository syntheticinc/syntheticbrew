export interface ThemeColors {
  bg: string;
  bgSecondary: string;
  bgTertiary: string;
  text: string;
  textSecondary: string;
  border: string;
  accent: string;
  accentHover: string;
  userBubble: string;
  userBubbleText: string;
  assistantBubble: string;
  assistantBubbleText: string;
  inputBg: string;
  inputBorder: string;
  inputBorderFocus: string;
  shadow: string;
  toolBg: string;
  toolBorder: string;
  codeBg: string;
  codeText: string;
  scrollbarThumb: string;
  scrollbarTrack: string;
}

const lightTheme: ThemeColors = {
  bg: '#ffffff',
  bgSecondary: '#f9fafb',
  bgTertiary: '#f3f4f6',
  text: '#111827',
  textSecondary: '#6b7280',
  border: '#e5e7eb',
  accent: '#2563eb',
  accentHover: '#1d4ed8',
  userBubble: '#2563eb',
  userBubbleText: '#ffffff',
  assistantBubble: '#f3f4f6',
  assistantBubbleText: '#111827',
  inputBg: '#ffffff',
  inputBorder: '#d1d5db',
  inputBorderFocus: '#2563eb',
  shadow: 'rgba(0, 0, 0, 0.15)',
  toolBg: '#f0f9ff',
  toolBorder: '#bae6fd',
  codeBg: '#f3f4f6',
  codeText: '#e11d48',
  scrollbarThumb: '#d1d5db',
  scrollbarTrack: 'transparent',
};

const darkTheme: ThemeColors = {
  bg: '#1f2937',
  bgSecondary: '#111827',
  bgTertiary: '#374151',
  text: '#f9fafb',
  textSecondary: '#9ca3af',
  border: '#374151',
  accent: '#3b82f6',
  accentHover: '#2563eb',
  userBubble: '#3b82f6',
  userBubbleText: '#ffffff',
  assistantBubble: '#374151',
  assistantBubbleText: '#f9fafb',
  inputBg: '#111827',
  inputBorder: '#4b5563',
  inputBorderFocus: '#3b82f6',
  shadow: 'rgba(0, 0, 0, 0.4)',
  toolBg: '#1e3a5f',
  toolBorder: '#2563eb',
  codeBg: '#374151',
  codeText: '#fb7185',
  scrollbarThumb: '#4b5563',
  scrollbarTrack: 'transparent',
};

export function getTheme(name: string, primaryColor?: string | null): ThemeColors {
  const base = name === 'dark' ? { ...darkTheme } : { ...lightTheme };
  if (primaryColor) {
    base.accent = primaryColor;
    base.accentHover = primaryColor;
    base.userBubble = primaryColor;
    base.inputBorderFocus = primaryColor;
  }
  return base;
}

export function buildStyles(t: ThemeColors, position: string): string {
  const isLeft = position === 'bottom-left';
  const posRule = isLeft ? 'left: 24px;' : 'right: 24px;';
  const panelPosRule = isLeft ? 'left: 24px;' : 'right: 24px;';

  return `
    :host {
      all: initial;
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
      font-size: 14px;
      line-height: 1.5;
      color: ${t.text};
    }

    *, *::before, *::after {
      box-sizing: border-box;
      margin: 0;
      padding: 0;
    }

    /* ── Bubble ── */
    .bb-bubble {
      position: fixed;
      bottom: 24px;
      ${posRule}
      width: 56px;
      height: 56px;
      border-radius: 50%;
      background: ${t.accent};
      color: #fff;
      border: none;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 4px 12px ${t.shadow};
      transition: transform 0.2s ease, background 0.2s ease;
      z-index: 2147483647;
    }
    .bb-bubble:hover {
      transform: scale(1.1);
      background: ${t.accentHover};
    }
    .bb-bubble:focus-visible {
      outline: 2px solid ${t.accent};
      outline-offset: 2px;
    }
    .bb-bubble svg {
      width: 24px;
      height: 24px;
      fill: currentColor;
    }

    /* ── Panel ── */
    .bb-panel {
      position: fixed;
      bottom: 96px;
      ${panelPosRule}
      width: 400px;
      height: 520px;
      max-height: 90vh;
      background: ${t.bg};
      border: 1px solid ${t.border};
      border-radius: 12px;
      box-shadow: 0 8px 30px ${t.shadow};
      display: flex;
      flex-direction: column;
      overflow: hidden;
      z-index: 2147483646;
      animation: bb-slide-in 0.2s ease-out;
    }
    .bb-panel[aria-hidden="true"] {
      display: none;
    }

    @keyframes bb-slide-in {
      from { opacity: 0; transform: translateY(12px); }
      to { opacity: 1; transform: translateY(0); }
    }

    /* ── Header ── */
    .bb-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 12px 16px;
      background: ${t.bgSecondary};
      border-bottom: 1px solid ${t.border};
      flex-shrink: 0;
    }
    .bb-header-title {
      font-size: 15px;
      font-weight: 600;
      color: ${t.text};
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .bb-attribution {
      font-size: 11px;
      opacity: 0.75;
      color: inherit;
      text-decoration: none;
      white-space: nowrap;
      flex-shrink: 0;
      margin-left: 8px;
      /* Auto right margin absorbs the header's free space so the badge hugs
         the title while the close button stays pinned right. */
      margin-right: auto;
    }
    .bb-attribution:hover {
      text-decoration: underline;
    }
    .bb-close-btn {
      background: none;
      border: none;
      cursor: pointer;
      color: ${t.textSecondary};
      padding: 4px;
      border-radius: 4px;
      display: flex;
      align-items: center;
      justify-content: center;
      transition: color 0.15s ease, background 0.15s ease;
    }
    .bb-close-btn:hover {
      color: ${t.text};
      background: ${t.bgTertiary};
    }
    .bb-close-btn:focus-visible {
      outline: 2px solid ${t.accent};
      outline-offset: 1px;
    }
    .bb-close-btn svg {
      width: 18px;
      height: 18px;
    }

    /* ── Messages ── */
    .bb-messages {
      flex: 1;
      overflow-y: auto;
      padding: 16px;
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .bb-messages::-webkit-scrollbar {
      width: 6px;
    }
    .bb-messages::-webkit-scrollbar-track {
      background: ${t.scrollbarTrack};
    }
    .bb-messages::-webkit-scrollbar-thumb {
      background: ${t.scrollbarThumb};
      border-radius: 3px;
    }

    .bb-msg {
      max-width: 85%;
      padding: 10px 14px;
      border-radius: 12px;
      word-wrap: break-word;
      overflow-wrap: break-word;
    }
    .bb-msg-user {
      align-self: flex-end;
      background: ${t.userBubble};
      color: ${t.userBubbleText};
      border-bottom-right-radius: 4px;
    }
    .bb-msg-assistant {
      align-self: flex-start;
      background: ${t.assistantBubble};
      color: ${t.assistantBubbleText};
      border-bottom-left-radius: 4px;
    }

    /* ── Markdown inside messages ── */
    .bb-msg-assistant strong {
      font-weight: 600;
    }
    .bb-msg-assistant code {
      background: ${t.codeBg};
      color: ${t.codeText};
      padding: 1px 5px;
      border-radius: 4px;
      font-family: 'SF Mono', Menlo, Consolas, monospace;
      font-size: 0.9em;
    }
    .bb-msg-assistant pre {
      background: ${t.codeBg};
      border-radius: 6px;
      padding: 10px 12px;
      overflow-x: auto;
      margin: 6px 0;
    }
    .bb-msg-assistant pre code {
      background: none;
      color: ${t.text};
      padding: 0;
      font-size: 0.85em;
    }
    .bb-msg-assistant ul {
      margin: 4px 0;
      padding-left: 20px;
    }
    .bb-msg-assistant li {
      margin: 2px 0;
    }
    .bb-msg-assistant a {
      color: ${t.accent};
      text-decoration: underline;
    }
    .bb-msg-assistant a:hover {
      text-decoration: none;
    }

    /* ── Tool call ── */
    .bb-tool {
      align-self: flex-start;
      max-width: 85%;
      border: 1px solid ${t.toolBorder};
      background: ${t.toolBg};
      border-radius: 8px;
      overflow: hidden;
    }
    .bb-tool-header {
      display: flex;
      align-items: center;
      gap: 6px;
      padding: 8px 12px;
      cursor: pointer;
      font-size: 12px;
      color: ${t.textSecondary};
      user-select: none;
    }
    .bb-tool-header:hover {
      background: ${t.border};
    }
    .bb-tool-spinner {
      width: 14px;
      height: 14px;
      border: 2px solid ${t.border};
      border-top-color: ${t.accent};
      border-radius: 50%;
      animation: bb-spin 0.8s linear infinite;
    }
    @keyframes bb-spin {
      to { transform: rotate(360deg); }
    }
    .bb-tool-check {
      color: #16a34a;
      font-size: 14px;
    }
    .bb-tool-body {
      display: none;
      padding: 8px 12px;
      border-top: 1px solid ${t.toolBorder};
      font-size: 12px;
      color: ${t.textSecondary};
      max-height: 120px;
      overflow-y: auto;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .bb-tool-body.bb-expanded {
      display: block;
    }

    /* ── Typing indicator ── */
    .bb-typing {
      align-self: flex-start;
      display: flex;
      gap: 4px;
      padding: 10px 14px;
      background: ${t.assistantBubble};
      border-radius: 12px;
      border-bottom-left-radius: 4px;
    }
    .bb-typing-dot {
      width: 6px;
      height: 6px;
      border-radius: 50%;
      background: ${t.textSecondary};
      animation: bb-bounce 1.4s ease-in-out infinite;
    }
    .bb-typing-dot:nth-child(2) { animation-delay: 0.2s; }
    .bb-typing-dot:nth-child(3) { animation-delay: 0.4s; }
    @keyframes bb-bounce {
      0%, 60%, 100% { transform: translateY(0); }
      30% { transform: translateY(-4px); }
    }

    /* ── Input area ── */
    .bb-input-area {
      display: flex;
      align-items: flex-end;
      gap: 8px;
      padding: 12px 16px;
      border-top: 1px solid ${t.border};
      background: ${t.bgSecondary};
      flex-shrink: 0;
    }
    .bb-input {
      flex: 1;
      resize: none;
      border: 1px solid ${t.inputBorder};
      border-radius: 8px;
      padding: 8px 12px;
      font-family: inherit;
      font-size: 14px;
      line-height: 1.4;
      color: ${t.text};
      background: ${t.inputBg};
      outline: none;
      max-height: 100px;
      overflow-y: auto;
      transition: border-color 0.15s ease;
    }
    .bb-input::placeholder {
      color: ${t.textSecondary};
    }
    .bb-input:focus {
      border-color: ${t.inputBorderFocus};
    }
    .bb-send-btn {
      width: 36px;
      height: 36px;
      border-radius: 8px;
      border: none;
      background: ${t.accent};
      color: #fff;
      cursor: pointer;
      display: flex;
      align-items: center;
      justify-content: center;
      flex-shrink: 0;
      transition: background 0.15s ease, opacity 0.15s ease;
    }
    .bb-send-btn:hover:not(:disabled) {
      background: ${t.accentHover};
    }
    .bb-send-btn:disabled {
      opacity: 0.5;
      cursor: not-allowed;
    }
    .bb-send-btn:focus-visible {
      outline: 2px solid ${t.accent};
      outline-offset: 2px;
    }
    .bb-send-btn svg {
      width: 18px;
      height: 18px;
    }

    /* ── Mobile responsive ── */
    @media (max-width: 480px) {
      .bb-panel {
        width: calc(100vw - 16px);
        height: calc(100vh - 100px);
        left: 8px;
        right: 8px;
        bottom: 80px;
        border-radius: 12px;
      }
    }
  `;
}
