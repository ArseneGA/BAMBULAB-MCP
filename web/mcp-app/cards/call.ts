import { useCallback, useRef, useState } from 'react';
import { isRec, toolText } from './format';

/** Ce dont les cartes ont besoin de l'hôte : appeler un outil du serveur (tools/call proxifié). */
export interface CardApp { callServerTool(p: { name: string; arguments: Record<string, unknown> }): Promise<unknown> }

export interface CardProps<T> {
  app: CardApp;
  data: T;
  /** Ouvre un autre résultat d'outil dans la vue (navigation avec retour, gérée par main.tsx). */
  onOpen: (result: unknown) => void;
}

/** Appel d'outil depuis une carte : une seule requête à la fois, erreur affichable, résultat transmis à onOpen. */
export function useToolCall(app: CardApp, onOpen?: (r: unknown) => void) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string>();
  const live = useRef(true);
  const run = useCallback(async (key: string, name: string, args: Record<string, unknown>): Promise<unknown | undefined> => {
    setBusy(key); setError(undefined);
    try {
      const r = await app.callServerTool({ name, arguments: args });
      if (isRec(r) && r.isError) { setError(toolText(r) || `${name} a renvoyé une erreur.`); return undefined; }
      onOpen?.(r);
      return r;
    } catch (e) {
      setError((e as Error).message || `${name} : échec de l'appel.`);
      return undefined;
    } finally { if (live.current) setBusy(null); }
  }, [app, onOpen]);
  return { busy, error, run, clearError: () => setError(undefined) };
}
