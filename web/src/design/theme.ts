import { useCallback, useEffect, useState } from 'react';

export type Theme = 'light' | 'dark';
const KEY = 'atelier-theme';

function initial(): Theme {
  try {
    const s = localStorage.getItem(KEY);
    if (s === 'light' || s === 'dark') return s;
  } catch { /* stockage indisponible */ }
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(initial);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try { localStorage.setItem(KEY, theme); } catch { /* ignore */ }
  }, [theme]);
  const toggle = useCallback(() => setTheme((t) => (t === 'light' ? 'dark' : 'light')), []);
  return { theme, setTheme, toggle };
}

/** Jetons exposés pour la page /design (valeurs documentaires, la source reste tokens.css). */
export const spacingScale = [1, 2, 3, 4, 5, 6, 8, 10, 12].map((n) => ({ name: `--sp-${n}`, px: n * 4 }));
export const radii = ['--r-1', '--r-2', '--r-3'];
export const colorTokens = ['--bg', '--surface', '--surface-2', '--surface-3', '--line', '--line-strong', '--text', '--text-2', '--text-3', '--accent', '--ok', '--warn', '--err', '--off'];
