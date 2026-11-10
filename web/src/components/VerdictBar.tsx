import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Check } from 'lucide-react';
import { Button, useToast } from '@shared/components';
import { describeError } from '../api/client';
import { REASON_LABEL, VERDICT_LABEL, projectsApi } from '../api/projectsTypes';
import type { Verdict } from '../api/projectsTypes';

/**
 * Verdict en un geste (G9 PJ11, G10 UX6) : trois boutons ; « Raté » demande la raison (puces). Le verdict est le jugement de l'humain :
 * il n'a AUCUN effet sur l'imprimante, la file ni la confirmation de lancement. Après l'enregistrement, le conteneur affiche « Annuler » 8 s
 * (useVerdictFlow). Les libellés viennent de projectsTypes (VERDICT_LABEL, REASON_LABEL).
 */
export const UNDO_MS = 8000;
/** Vocabulaire fermé des raisons (celui du serveur) : repli quand la liste n'a pas été chargée. */
export const DEFAULT_REASONS = Object.keys(REASON_LABEL);

export function VerdictBar({ label, reasons = DEFAULT_REASONS, busy, onGive }: { label: string; reasons?: string[]; busy?: boolean; onGive: (v: Verdict, reason?: string) => void }) {
  const [asking, setAsking] = useState(false);
  return (
    <div className="vb" role="group" aria-label={`Verdict pour ${label}`}>
      <div className="vb-btns">
        <Button size="sm" variant="primary" icon={<Check size={14} aria-hidden />} disabled={busy} onClick={() => { setAsking(false); onGive('success'); }}>Réussi</Button>
        <Button size="sm" disabled={busy} aria-expanded={asking} onClick={() => setAsking((a) => !a)}>Raté</Button>
        <Button size="sm" disabled={busy} onClick={() => { setAsking(false); onGive('redo'); }}>À refaire</Button>
      </div>
      {asking && (
        <div className="vb-reasons" role="group" aria-label="Raison de l'échec">
          <span className="vb-q">Pourquoi ?</span>
          {reasons.map((r) => <button key={r} type="button" className="chip" disabled={busy} onClick={() => { setAsking(false); onGive('failed', r); }}>{REASON_LABEL[r] ?? r}</button>)}
        </div>
      )}
    </div>
  );
}

interface Undo { id: string; label: string; verdict: Verdict; prev?: Verdict }

/**
 * Enregistre un verdict puis propose « Annuler » pendant 8 s. Annuler restaure le verdict précédent, ou retire le verdict s'il n'y en avait pas
 * (l'impression redevient « non jugée »). `bar` est à rendre une fois dans la page ; `onChange` recharge les listes.
 */
export function useVerdictFlow(onChange: () => void): { give: (id: string, label: string, v: Verdict, reason?: string) => Promise<void>; bar: ReactNode; busy: boolean } {
  const toast = useToast();
  const [undo, setUndo] = useState<Undo | null>(null);
  const [busy, setBusy] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const give = useCallback(async (id: string, label: string, v: Verdict, reason?: string) => {
    setBusy(true);
    try {
      const r = await projectsApi.setVerdict(id, v, reason);
      window.clearTimeout(timer.current);
      setUndo({ id, label, verdict: v, prev: r.changed ? r.previous_verdict : undefined });
      timer.current = window.setTimeout(() => setUndo(null), UNDO_MS);
      onChange();
    } catch (e) { toast('err', describeError(e)); } finally { setBusy(false); }
  }, [onChange, toast]);
  const cancel = async () => {
    if (!undo) return;
    const u = undo; window.clearTimeout(timer.current); setUndo(null);
    try {
      if (u.prev) await projectsApi.setVerdict(u.id, u.prev); else await projectsApi.clearVerdict(u.id);
      toast('info', 'Verdict annulé.');
      onChange();
    } catch (e) { toast('err', describeError(e)); }
  };
  const bar = undo && (
    <div className="undo-bar" role="status" aria-live="polite">
      <span>Verdict enregistré · {undo.label} : {VERDICT_LABEL[undo.verdict].toLowerCase()}</span>
      <Button size="sm" onClick={cancel}>Annuler</Button>
    </div>
  );
  return { give, bar, busy };
}
