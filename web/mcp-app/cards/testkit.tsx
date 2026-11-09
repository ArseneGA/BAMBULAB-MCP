// Outillage commun des tests de cartes (hôte simulé, rendu, clic). Pas un fichier de test.
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { vi } from 'vitest';
import type { CardApp } from './call';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

export type Handler = (args: Record<string, unknown>) => unknown;
export function mockApp(handlers: Record<string, Handler> = {}) {
  return {
    callServerTool: vi.fn(async (p: { name: string; arguments: Record<string, unknown> }) => {
      const h = handlers[p.name];
      if (!h) throw new Error(`outil inattendu ${p.name}`);
      return h(p.arguments);
    }),
  } satisfies CardApp;
}
export const ok = (structuredContent: unknown, text = 'ok') => ({ structuredContent, content: [{ type: 'text', text }] });

export function mount() {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const root: Root = createRoot(host);
  return {
    host,
    render: async (el: JSX.Element) => { await act(async () => { root.render(el); }); },
    text: () => host.textContent ?? '',
    /** Clique le premier bouton dont le texte OU l'étiquette accessible correspond. */
    click: async (label: RegExp) => {
      const b = [...host.querySelectorAll('button')].find((x) => label.test(x.textContent ?? '') || label.test(x.getAttribute('aria-label') ?? ''));
      if (!b) throw new Error(`bouton ${String(label)} introuvable : ${host.textContent}`);
      await act(async () => { b.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
    },
    unmount: () => { act(() => root.unmount()); host.remove(); },
  };
}
