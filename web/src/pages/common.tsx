import { useEffect, useRef } from 'react';
import type { ReactNode } from 'react';
import { AlertTriangle, Construction, ServerOff, X } from 'lucide-react';
import { Button, ErrorState, IconButton, LoadingState } from '../components';
import { describeError, isOffline, useConnection } from '../api/hooks';
import type { AsyncState } from '../api/hooks';
import { pendingFor } from '../api/pending';
import type { PendingNeed } from '../api/pending';
import './common.css';

/* ---------- formats ---------- */
export const fmtBytes = (n: number) => {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1).replace('.', ',')} Gio`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1).replace('.', ',')} Mio`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1).replace('.', ',')} Kio`;
  return `${n} o`;
};
export const fmtNum = (n: number, d = 1) => n.toLocaleString('fr-FR', { maximumFractionDigits: d });
export const fmtDate = (iso?: string) => {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString('fr-FR', { day: '2-digit', month: 'short', year: 'numeric' }) + ' ' + d.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' });
};
export const fmtDims = (d?: { x: number; y: number; z: number }) => (d ? `${fmtNum(d.x)} × ${fmtNum(d.y)} × ${fmtNum(d.z)} mm` : 'dimensions inconnues');
export const shortSha = (s: string) => s.slice(0, 10);

/* ---------- vignette honnête : le format du fichier, pas une fausse image ---------- */
export function FormatBadge({ formats, size = 40 }: { formats: string[]; size?: number }) {
  const f = (formats[0] ?? '?').toUpperCase();
  return <span className="thumb fmt-badge" style={{ width: size, height: size }} aria-hidden>{f}</span>;
}

/* ---------- États d'une requête ---------- */
export function OfflineNotice({ onRetry }: { onRetry?: () => void }) {
  return (
    <div className="state state-err" role="alert">
      <ServerOff size={18} aria-hidden />
      <h3>bambud est injoignable</h3>
      <p>Le service ne répond pas. Vérifiez qu'il est démarré (<span className="mono">bambud</span>), puis réessayez. Aucune donnée n'est perdue : le catalogue est sur le serveur.</p>
      {onRetry && <Button size="sm" onClick={onRetry}>Réessayer</Button>}
    </div>
  );
}

/** Rend le contenu une fois la requête réussie ; sinon chargement ou erreur (hors ligne distinguée). */
export function Async<T>({ state, children, label = 'Chargement', empty }: { state: AsyncState<T>; children: (d: T) => ReactNode; label?: string; empty?: (d: T) => ReactNode | null }) {
  if (state.error && state.data === undefined) {
    if (isOffline(state.error)) return <OfflineNotice onRetry={state.reload} />;
    return <ErrorState title="La requête a échoué" text={describeError(state.error)} action={<Button size="sm" onClick={state.reload}>Réessayer</Button>} />;
  }
  if (state.data === undefined) return <LoadingState label={label} />;
  const e = empty?.(state.data);
  return <>{e ?? children(state.data)}</>;
}

/* ---------- bandeau « bientôt branché » ---------- */
export function PendingBanner({ screen, extra, partial }: { screen: PendingNeed['screen']; extra?: ReactNode; partial?: boolean }) {
  const needs = pendingFor(screen);
  return (
    <aside className="pending" role="note" aria-label="Fonction bientôt branchée">
      <Construction size={16} aria-hidden />
      <div>
        {partial ? <><strong>Branché sur le serveur.</strong> Reste à fournir côté serveur :</> : <><strong>Bientôt branché.</strong> Cet écran affiche des données de démonstration : le serveur n'expose pas encore ces fonctions. Aucune action n'est réelle.</>}
        {extra}
        <details>
          <summary>{partial ? "Limites actuelles et besoins serveur" : "Ce que le serveur doit fournir"} ({needs.length})</summary>
          <ul>{needs.map((n) => <li key={n.id}><span className="mono">{n.id}</span> — {n.needs}</li>)}</ul>
        </details>
      </div>
    </aside>
  );
}

/* ---------- bandeau d'avertissement ---------- */
export function Notice({ tone = 'warn', icon, title, children, actions }: { tone?: 'warn' | 'err' | 'info'; icon?: ReactNode; title: string; children?: ReactNode; actions?: ReactNode }) {
  return (
    <div className={`notice${tone === 'info' ? '' : ` notice-${tone}`}`} role={tone === 'err' ? 'alert' : 'status'}>
      {icon ?? <AlertTriangle size={16} aria-hidden />}
      <div>
        <h3>{title}</h3>
        {children && <p>{children}</p>}
        {actions && <div className="acts">{actions}</div>}
      </div>
    </div>
  );
}

/* ---------- boîte de dialogue (élément <dialog> natif : focus et Échap gérés) ---------- */
export function Dialog({ open, onClose, title, children, wide, footer }: { open: boolean; onClose: () => void; title: string; children: ReactNode; wide?: boolean; footer?: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current; if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog ref={ref} className={`dlg${wide ? ' dlg-wide' : ''}`} aria-label={title} onClose={onClose} onCancel={(e) => { e.preventDefault(); onClose(); }}
      onClick={(e) => { if (e.target === ref.current) onClose(); }}>
      {open && (
        <div className="dlg-box">
          <header className="dlg-head"><h2>{title}</h2><IconButton label="Fermer" onClick={onClose}><X size={16} /></IconButton></header>
          <div className="dlg-body">{children}</div>
          {footer && <footer className="dlg-foot">{footer}</footer>}
        </div>
      )}
    </dialog>
  );
}

/* ---------- pastille « connexion » pour les bandeaux ---------- */
export function ServerBanner() {
  const c = useConnection();
  if (c.server !== 'offline') return null;
  return (
    <div className="server-banner" role="alert">
      <ServerOff size={14} aria-hidden />
      <span>bambud est injoignable : les données affichées peuvent être périmées et les actions sont indisponibles.</span>
      <Button size="sm" variant="ghost" onClick={c.recheck}>Réessayer</Button>
    </div>
  );
}
