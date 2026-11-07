import type { Capabilities } from './types';

/** État de la liaison Windows : `unused` tant qu'aucun agent n'a jamais été appairé (mode Linux seul : normal). */
export type WindowsLink = 'online' | 'offline' | 'unused' | 'unavailable';

export function windowsLink(serverOnline: boolean, caps?: Capabilities): WindowsLink {
  if (!serverOnline) return 'unavailable';
  const agents = caps?.agents ?? [];
  if (agents.some((a) => a.online)) return 'online';
  if (agents.length > 0) return 'offline';
  const win = caps?.features.find((f) => f.id === 'windows_agent');
  return win?.available ? 'online' : 'unused';
}

/** Page humaine à ouvrir en https : seulement si la SPA tourne en http hors loopback. */
export function insecureOrigin(loc: { protocol: string; hostname: string }): boolean {
  if (loc.protocol !== 'http:') return false;
  const h = loc.hostname.replace(/^\[|\]$/g, '').toLowerCase();
  return !(h === 'localhost' || h.endsWith('.localhost') || h === '::1' || /^127\./.test(h));
}
