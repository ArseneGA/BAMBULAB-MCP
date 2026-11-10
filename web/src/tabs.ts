import { useCallback } from 'react';
import { useSearchParams } from 'react-router-dom';
import { tabsOf } from './nav';

/**
 * Onglet courant d'une page, porté par l'URL (?vue=…) : recharger, partager le lien ou revenir en arrière conserve l'onglet.
 * Les onglets viennent de nav.ts (une seule déclaration). Le premier est le défaut (ou `fallback` si l'écran en choisit un selon l'état).
 * Les autres paramètres de la requête sont conservés.
 */
export function useTab(routePath: string, fallback?: string): [string, (v: string) => void] {
  const tabs = tabsOf(routePath);
  const [sp, setSp] = useSearchParams();
  const asked = sp.get('vue');
  const def = fallback && tabs.some((t) => t.value === fallback) ? fallback : tabs[0]?.value ?? '';
  const tab = tabs.some((t) => t.value === asked) ? (asked as string) : def;
  const setTab = useCallback((v: string) => {
    const next = new URLSearchParams(sp);
    next.set('vue', v);
    setSp(next);
  }, [sp, setSp]);
  return [tab, setTab];
}
