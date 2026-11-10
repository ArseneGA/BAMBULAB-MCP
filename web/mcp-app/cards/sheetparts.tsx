import { useState } from 'react';
import type { CardApp } from './call';
import { fmtDate, fmtSeconds, isRec, str } from './format';
import { LiteBadge, LiteButton, LiteState, type Tone } from './lite';
import type { Level, PhotoStep, PhotoStrip } from '../../src/api/sheets';

/**
 * Éléments communs des cartes Fiche pièce (part.tsx) et Fiche d'impression (print-sheet.tsx) : niveau d'une valeur
 * (mesuré / calculé / déclaré, en texte), bandeau de paliers du journal photo (10 % à 100 %) et visionneuse d'une photo.
 * Les images passent par l'outil réservé aux cartes get_sheet_photo (aucune I/O réseau dans l'iframe, aucune URL signée).
 */
export const LEVEL_FR: Record<string, string> = { measured: 'mesuré', computed: 'calculé', declared: 'déclaré' };
const LEVEL_TONE: Record<string, Tone> = { measured: 'ok', computed: 'run', declared: 'warn' };

export function LevelBadge({ level }: { level?: Level | string }) {
  if (!level) return null;
  return <LiteBadge tone={LEVEL_TONE[level] ?? 'off'} title={`Niveau : ${LEVEL_FR[level] ?? level}`}>{LEVEL_FR[level] ?? level}</LiteBadge>;
}

/** « non disponible (raison) » : jamais un 0 inventé. */
export const naText = (reason?: string): string => (reason ? `non disponible (${reason})` : 'non disponible');

export const durationText = (s?: number): string => fmtSeconds(s) ?? 'non disponible';

export const frDate = (iso?: string): string => fmtDate(iso) ?? 'date non disponible';

export const VERDICT_TXT: Record<string, string> = { success: 'réussie', failed: 'ratée', redo: 'à refaire' };
export const verdictTone = (v?: string): Tone => (v === 'success' ? 'ok' : v === 'failed' ? 'fail' : v === 'redo' ? 'warn' : 'off');

/** Image d'une photo chargée à la demande (get_sheet_photo) : data: URL construite depuis le contenu image du résultat. */
export function useSheetPhoto(app: CardApp) {
  const [shown, setShown] = useState<{ sha: string; src: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string>();
  const load = async (sha: string) => {
    setBusy(sha); setError(undefined);
    try {
      const r = await app.callServerTool({ name: 'get_sheet_photo', arguments: { sha256: sha } });
      const content = isRec(r) && Array.isArray(r.content) ? r.content : [];
      const img = content.find((c): c is { type: string; data: string; mimeType?: string } => isRec(c) && c.type === 'image' && typeof c.data === 'string');
      if (!img) { setError('photo non disponible (le serveur n\'a renvoyé aucune image)'); return; }
      setShown({ sha, src: `data:${str(img.mimeType) ?? 'image/jpeg'};base64,${img.data}` });
    } catch (e) {
      setError(`photo non disponible (${(e as Error).message || 'erreur'})`);
    } finally { setBusy(null); }
  };
  return { shown, busy, error, load, hide: () => setShown(null) };
}

/** Bandeau de paliers : une case par palier, avec son état écrit en toutes lettres (pas seulement une couleur). */
export function PhotoStripView({ app, strip, label }: { app: CardApp; strip: PhotoStrip; label: string }) {
  const ph = useSheetPhoto(app);
  const steps: PhotoStep[] = strip.steps;
  if (steps.length === 0) {
    const thumb = strip.thumbnail;
    return (
      <div style={{ display: 'grid', gap: 6 }}>
        <p className="lc-note">{strip.note ?? 'photo non disponible'}</p>
        {thumb?.sha256 && <div><LiteButton busy={ph.busy === thumb.sha256} disabled={ph.busy !== null} onClick={() => void ph.load(thumb.sha256!)}>Voir la photo de fin</LiteButton></div>}
        {ph.shown && <img src={ph.shown.src} alt="Photo de fin d'impression" style={{ maxWidth: '100%', borderRadius: 8 }} />}
        {ph.error && <LiteState kind="error">{ph.error}</LiteState>}
      </div>
    );
  }
  return (
    <div style={{ display: 'grid', gap: 6 }}>
      <ol className="lc-chips" aria-label={label} style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {steps.map((s) => (
          <li key={s.percent} style={{ display: 'grid', gap: 2, minWidth: 64, padding: '4px 6px', border: '1px solid var(--c-line)', borderRadius: 8 }}>
            <b style={{ fontSize: 12 }}>{s.percent} %</b>
            {s.status === 'ok' && s.sha256
              ? <button type="button" className="lc-note" aria-label={`Voir la photo du palier ${s.percent} %`} disabled={ph.busy !== null} aria-busy={ph.busy === s.sha256 || undefined}
                  onClick={() => void ph.load(s.sha256!)} style={{ all: 'unset', cursor: 'pointer', textDecoration: 'underline', fontSize: 11.5 }}>photo</button>
              : s.status === 'unavailable'
                ? <span className="lc-note" title={s.reason}>photo non disponible{s.reason ? ` (${s.reason})` : ''}</span>
                : <span className="lc-dim" style={{ fontSize: 11.5 }}>non relevé</span>}
          </li>
        ))}
      </ol>
      {ph.shown && (
        <figure style={{ margin: 0, display: 'grid', gap: 4 }}>
          <img src={ph.shown.src} alt="Photo du journal" style={{ maxWidth: '100%', borderRadius: 8 }} />
          <figcaption><LiteButton onClick={ph.hide}>Masquer la photo</LiteButton></figcaption>
        </figure>
      )}
      {ph.error && <LiteState kind="error">{ph.error}</LiteState>}
      <p className="lc-note">{strip.available} palier(s) avec photo sur {steps.length}.</p>
    </div>
  );
}
