import { useEffect, useRef } from 'react';
import type { ReactNode } from 'react';
import { AlertTriangle, Construction, ServerOff, X } from 'lucide-react';
import { Button, ErrorState, IconButton, Skeleton } from '../components';
import { describeError, isOffline, useConnection } from '../api/hooks';
import type { AsyncState } from '../api/hooks';
import { insecureOrigin } from '../api/windowsLink';
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
    <ErrorState title="Printalk est injoignable" text="Le service ne répond pas pour l'instant."
      impact="Les données affichées peuvent être périmées et les actions sont indisponibles. Aucune donnée n'est perdue : le catalogue est sur le serveur."
      action={onRetry && <Button size="sm" onClick={onRetry}>Réessayer</Button>}
      details="Vérifiez que le service est démarré sur la machine qui l'héberge, puis réessayez." />
  );
}

/**
 * Rend le contenu une fois la requête réussie ; sinon un squelette (chargement) ou une erreur en trois parties
 * (ce qui s'est passé, ce que ça change, ce qu'on peut faire ; détail technique replié). Hors ligne distingué.
 */
export function Async<T>({ state, children, label = 'Chargement', empty }: { state: AsyncState<T>; children: (d: T) => ReactNode; label?: string; empty?: (d: T) => ReactNode | null }) {
  if (state.error && state.data === undefined) {
    if (isOffline(state.error)) return <OfflineNotice onRetry={state.reload} />;
    return <ErrorState title="Cette partie n'a pas pu se charger" text="La requête a échoué." impact="Le reste de la page reste utilisable ; rien n'a été modifié." action={<Button size="sm" onClick={state.reload}>Réessayer</Button>} details={describeError(state.error)} />;
  }
  if (state.data === undefined) return <Skeleton lines={3} label={label} />;
  const e = empty?.(state.data);
  return <>{e ?? children(state.data)}</>;
}

/* ---------- bandeau « bientôt branché » ---------- */
export function PendingBanner({ screen, extra }: { screen: PendingNeed['screen']; extra?: ReactNode }) {
  const needs = pendingFor(screen);
  if (needs.length === 0) return null;
  return (
    <aside className="pending" role="note" aria-label="Limite connue">
      <Construction size={16} aria-hidden />
      <div>
        <strong>Limite connue.</strong> {needs.map((n) => n.today).join(' ')}
        {extra}
        <details>
          <summary>Pourquoi, et ce qui manque côté serveur ({needs.length})</summary>
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

/* ---------- écran « Enregistrer ce navigateur » (jeton d'API configuré, navigateur inconnu) ---------- */
/** Adresse de la page d'enregistrement, qui ramène ensuite à l'écran courant. */
export const enrollUrl = (path: string) => `/enroll?next=${encodeURIComponent(path)}`;

export function EnrollGate() {
  const c = useConnection();
  const here = typeof window === 'undefined' ? '/' : window.location.pathname + window.location.search;
  return (
    <div className="enroll-gate" role="alert">
      <ErrorState
        title="Enregistrer ce navigateur"
        text="Ce serveur est protégé par un jeton d'API : l'interface web n'y accède que depuis un navigateur enregistré."
        impact="Tant que ce navigateur n'est pas enregistré, les écrans restent vides et aucun lancement ne peut être approuvé."
        action={<div className="enroll-gate-actions"><a className="enroll" href={enrollUrl(here)}>Enregistrer ce navigateur</a><Button variant="ghost" onClick={c.recheck}>J'ai terminé, réessayer</Button></div>}
        details="Sur le poste qui fait tourner le service, lancez la commande « bambud enroll » dans un terminal : un code à 8 caractères s'affiche (il n'est jamais transmis à Claude). Saisissez-le sur la page d'enregistrement."
      />
    </div>
  );
}

/* ---------- pastille « connexion » pour les bandeaux ---------- */
/** SPA en http hors loopback : les pages humaines (enregistrement, confirmation) exigent https. */
export function HttpsBanner() {
  const c = useConnection();
  if (c.httpsUrl === undefined || !insecureOrigin(window.location)) return null;
  const href = c.httpsUrl ? c.httpsUrl + window.location.pathname + window.location.search : '';
  return (
    <div className="server-banner https-banner" role="status">
      <span>Connexion non chiffrée : les pages d'enregistrement et de confirmation exigent HTTPS.</span>
      {href ? <a href={href}>Ouvrir en HTTPS ({c.httpsUrl})</a> : <span>Aucune URL https configurée (BAMBU_PUBLIC_URL) : utilisez <code>tailscale serve</code> ou localhost.</span>}
    </div>
  );
}

export function ServerBanner() {
  const c = useConnection();
  if (c.server !== 'offline') return null;
  return (
    <div className="server-banner" role="alert">
      <ServerOff size={14} aria-hidden />
      <span>Printalk est injoignable : les données affichées peuvent être périmées et les actions sont indisponibles.</span>
      <Button size="sm" variant="ghost" onClick={c.recheck}>Réessayer</Button>
    </div>
  );
}
