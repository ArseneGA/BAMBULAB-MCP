import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { api, describeError, ApiError, BASE } from './client';
import type { Capabilities, Health } from './types';

/* ---------- Requête asynchrone avec annulation ---------- */
export interface AsyncState<T> {
  data: T | undefined;
  error: ApiError | Error | undefined;
  loading: boolean;
  /** Relance la requête (conserve les données précédentes pendant le rechargement). */
  reload: () => void;
}

export function useAsync<T>(fn: (signal: AbortSignal) => Promise<T>, deps: unknown[]): AsyncState<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<ApiError | Error>();
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const fnRef = useRef(fn); fnRef.current = fn;
  useEffect(() => {
    const ac = new AbortController();
    setLoading(true);
    fnRef.current(ac.signal).then((d) => { setData(d); setError(undefined); setLoading(false); }, (e) => {
      if ((e as Error).name === 'AbortError') return;
      setError(e as Error); setLoading(false);
    });
    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);
  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, loading, reload };
}

export const isOffline = (e: unknown) => e instanceof ApiError && e.offline;
export { describeError };

/* ---------- Connexion : bambud, capacités, Windows, CAO ---------- */
export type Link = 'online' | 'offline' | 'unavailable';
export interface Connection {
  /** bambud répond (/health). `checking` tant que la première réponse n'est pas arrivée. */
  server: 'checking' | 'online' | 'offline';
  health?: Health;
  capabilities?: Capabilities;
  windows: Link;
  /** Agents Windows appairés (capabilities.agents) ; vide sans agent. */
  agents: NonNullable<Capabilities['agents']>;
  cad: Link;
  cadNote?: string;
  /** Navigateur enregistré auprès du serveur (GET /api/session) ; undefined tant que la route n'existe pas ou n'a pas répondu. */
  browserEnrolled?: boolean;
  /** L'API répond 401 : un jeton d'API est configuré et ce navigateur n'est pas enregistré (voir EnrollGate). */
  authRequired: boolean;
  importEnabled: boolean;
  importNote?: string;
  recheck: () => void;
}

const Ctx = createContext<Connection>({ server: 'checking', windows: 'unavailable', agents: [], cad: 'unavailable', authRequired: false, importEnabled: false, recheck: () => {} });
export const useConnection = () => useContext(Ctx);

export function ConnectionProvider({ children }: { children: ReactNode }) {
  const [server, setServer] = useState<Connection['server']>('checking');
  const [health, setHealth] = useState<Health>();
  const [caps, setCaps] = useState<Capabilities>();
  const [enrolled, setEnrolled] = useState<boolean>();
  const [authRequired, setAuthRequired] = useState(false);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let alive = true;
    const ac = new AbortController();
    const poll = async () => {
      try {
        const h = await api.health(ac.signal);
        const c = await api.capabilities(ac.signal).catch((e: unknown) => e as Error);
        if (!alive) return;
        setAuthRequired(c instanceof ApiError && c.status === 401);
        const sess = await fetch(BASE + '/api/session', { signal: ac.signal, credentials: 'same-origin' }).then((r) => (r.ok ? (r.json() as Promise<Record<string, unknown>>) : undefined)).catch(() => undefined);
        if (!alive) return;
        const e = sess?.enrolled ?? sess?.registered;
        setEnrolled(typeof e === 'boolean' ? e : undefined);
        setHealth(h); setCaps(c instanceof Error ? undefined : c); setServer('online');
      } catch (e) {
        if (!alive || (e as Error).name === 'AbortError') return;
        setServer('offline');
      }
    };
    void poll();
    const t = window.setInterval(poll, 10_000);
    return () => { alive = false; ac.abort(); window.clearInterval(t); };
  }, [tick]);

  const value = useMemo<Connection>(() => {
    const feat = (id: string) => caps?.features.find((f) => f.id === id);
    const win = feat('windows_agent'), cad = feat('cad_worker'), imp = feat('catalog_import');
    return {
      server, health, capabilities: caps,
      windows: server !== 'online' ? 'unavailable' : (caps?.agents?.some((a) => a.online) || win?.available) ? 'online' : 'offline',
      agents: caps?.agents ?? [],
      cad: server !== 'online' ? 'unavailable' : cad?.available ? 'online' : 'unavailable',
      cadNote: cad?.note,
      browserEnrolled: enrolled, authRequired,
      importEnabled: !!imp?.available, importNote: imp?.note,
      recheck: () => setTick((t) => t + 1),
    };
  }, [server, health, caps, enrolled, authRequired]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}
