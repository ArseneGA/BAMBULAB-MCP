import type { ReactNode } from 'react';
import { formatBytesProgress, formatEta, normalizeProgress } from '../api/progress';
import type { Progress } from '../api/progress';

/**
 * Barre de progression d'un travail long : pourcentage, phase, temps restant (et octets si connus).
 * `source` est soit un Progress déjà normalisé, soit n'importe quel statut de travail (voir normalizeProgress).
 * Sans pourcentage connu la barre est indéterminée (animation douce), jamais un faux « 0 % ».
 */
export function ProgressBar({ source, label, large, hideEta, children }: { source: unknown; label: string; large?: boolean; hideEta?: boolean; children?: ReactNode }) {
  const p: Progress = isProgress(source) ? source : normalizeProgress(source);
  const known = p.percent !== undefined;
  const eta = hideEta ? undefined : formatEta(p.etaSeconds);
  const bytes = formatBytesProgress(p.doneBytes, p.totalBytes);
  const phase = p.queuePosition !== undefined ? `En file d'attente (position ${p.queuePosition})` : p.stalled ? 'Aucun signe de vie : le travail semble bloqué' : p.phaseLabel;
  const text = [known ? `${p.percent} %` : 'en cours', phase, eta && `reste ${eta}`].filter(Boolean).join(', ');
  const cls = ['pbar', large && 'pbar-lg', !known && !p.terminal && 'is-indet', p.failed && 'is-fail', p.stalled && 'is-warn'].filter(Boolean).join(' ');
  return (
    <div className={cls}>
      <div className="pbar-top">
        <span className="pbar-pct">{known ? `${p.percent} %` : '…'}{p.estimated && known && <span className="pbar-note"> (estimé)</span>}</span>
        <span className="pbar-phase">{phase}</span>
        <span className="pbar-eta">{[bytes, eta && `reste ${eta}`].filter(Boolean).join(' · ')}</span>
      </div>
      <div className="progress" role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={known ? p.percent : undefined} aria-valuetext={text}>
        <span style={{ width: `${p.percent ?? 0}%` }} />
      </div>
      {children}
    </div>
  );
}

const isProgress = (v: unknown): v is Progress => !!v && typeof v === 'object' && 'terminal' in v && 'failed' in v;
