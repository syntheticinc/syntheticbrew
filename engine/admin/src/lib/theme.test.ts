import { describe, it, expect, beforeEach } from 'vitest';
import { currentTheme, setTheme } from './theme';

function clearCookie() {
  document.cookie = 'sbrew_theme=; path=/; max-age=0';
}

beforeEach(() => {
  clearCookie();
  localStorage.clear();
  document.documentElement.classList.remove('light');
});

describe('theme', () => {
  it('defaults to dark when nothing is persisted', () => {
    expect(currentTheme()).toBe('dark');
    expect(document.documentElement.classList.contains('light')).toBe(false);
  });

  it('prefers the shared cookie over localStorage', () => {
    localStorage.setItem('syntheticbrew-theme', 'dark');
    document.cookie = 'sbrew_theme=light; path=/';
    expect(currentTheme()).toBe('light');
  });

  it('falls back to localStorage when no cookie is set', () => {
    localStorage.setItem('syntheticbrew-theme', 'light');
    expect(currentTheme()).toBe('light');
  });

  it('setTheme persists to cookie + storage and applies the class', () => {
    setTheme('light');
    expect(document.cookie).toContain('sbrew_theme=light');
    expect(localStorage.getItem('syntheticbrew-theme')).toBe('light');
    expect(document.documentElement.classList.contains('light')).toBe(true);

    setTheme('dark');
    expect(document.cookie).toContain('sbrew_theme=dark');
    expect(document.documentElement.classList.contains('light')).toBe(false);
  });

  it('ignores garbage cookie values', () => {
    document.cookie = 'sbrew_theme=purple; path=/';
    expect(currentTheme()).toBe('dark');
  });
});
