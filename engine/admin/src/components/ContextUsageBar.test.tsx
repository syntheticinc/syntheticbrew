import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import ContextUsageBar from './ContextUsageBar';

describe('ContextUsageBar', () => {
  it('renders nothing without a max context size', () => {
    const { container } = render(<ContextUsageBar maxContextTokens={null} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders the context usage indicator', () => {
    render(<ContextUsageBar maxContextTokens={128000} contextTokens={32000} />);
    expect(screen.getByText(/32K \/ 128K context/)).toBeInTheDocument();
  });

  it('shows the cached indicator when cachedTokens > 0', () => {
    render(<ContextUsageBar maxContextTokens={128000} contextTokens={32000} cachedTokens={4622} />);
    expect(screen.getByText('4.6K cached')).toBeInTheDocument();
  });

  it('hides the cached indicator when cachedTokens is null', () => {
    render(<ContextUsageBar maxContextTokens={128000} contextTokens={32000} cachedTokens={null} />);
    expect(screen.queryByText(/cached/)).not.toBeInTheDocument();
  });

  it('hides the cached indicator when cachedTokens is zero', () => {
    render(<ContextUsageBar maxContextTokens={128000} contextTokens={32000} cachedTokens={0} />);
    expect(screen.queryByText(/cached/)).not.toBeInTheDocument();
  });
});
