/**
 * Composants « Lite » des cartes MCP App (bibliothèque, comparaison, coûts, bobines, impressions, imprimantes).
 *
 * POINT DE FUSION : un socle design partagé (web/shared : tokens, Card, Button, Table, Badge, ProgressBar…) est construit
 * en parallèle. Ces composants locaux minces utilisent les mêmes jetons verts (#16a06b, #3ee39c, #095a40, fond #0f1714 / #f4f7f5,
 * DM Sans avec repli) et sont tous préfixés `Lite` (classes CSS `lc-*`, fichier lite.css) pour être remplacés un à un :
 *   LiteCard -> Card · LiteButton -> Button · LiteBadge -> Badge · LiteGauge -> ProgressBar · LiteTable -> Table ·
 *   LiteStat -> tuile de chiffre · LiteState -> états vide / chargement / erreur · LiteSwatch / LiteBox -> propres aux cartes.
 * À la fusion : remplacer les imports de './lite' par ceux du socle ; les vues (library, compare, costs, spools, prints,
 * printers) n'utilisent que ces noms et aucune autre feuille de style que lite.css.
 */
import { AlertTriangle, Inbox } from 'lucide-react';
import type { CSSProperties, ReactNode } from 'react';
import { Badge, Button, Card } from '@shared/components';
import { hueOf, safeColor } from './format';
import './lite.css';

export type Tone = 'ok' | 'warn' | 'fail' | 'run' | 'off' | 'accent';

export function LiteCard({ title, sub, aside, children, label, className = '' }: { title?: ReactNode; sub?: ReactNode; aside?: ReactNode; children: ReactNode; label?: string; className?: string }) {
  return (
    <Card className={`lc ${className}`.trim()} aria-label={label}>
      {(title || aside) && (
        <header className="lc-head">
          <div className="lc-titles">{title && <h1 className="lc-title">{title}</h1>}{sub && <p className="lc-sub">{sub}</p>}</div>
          {aside && <div className="lc-aside">{aside}</div>}
        </header>
      )}
      {children}
    </Card>
  );
}

export function LiteButton({ children, onClick, disabled, busy, kind = 'ghost', title, pressed, type = 'button', className = '' }: {
  children: ReactNode; onClick?: () => void; disabled?: boolean; busy?: boolean; kind?: 'primary' | 'ghost'; title?: string; pressed?: boolean; type?: 'button' | 'submit'; className?: string;
}) {
  return <Button type={type} variant={kind === 'primary' ? 'primary' : 'secondary'} className={className} onClick={onClick} disabled={disabled || busy} aria-busy={busy || undefined} aria-pressed={pressed} title={title}>{children}</Button>;
}

const BADGE_TONE = { ok: 'ok', warn: 'warn', fail: 'err', run: 'run', off: 'neutral', accent: 'run' } as const;
export function LiteBadge({ tone = 'off', children, title }: { tone?: Tone; children: ReactNode; title?: string }) {
  return <Badge tone={BADGE_TONE[tone]} dot={false} className={`lc-badge lc-t-${tone}`} title={title}>{children}</Badge>;
}

/** Jauge fine ; value en % (0-100) ou null (indéterminée). */
export function LiteGauge({ value, tone = 'run', label, height }: { value: number | null; tone?: Tone; label: string; height?: number }) {
  const v = value === null ? null : Math.max(0, Math.min(100, value));
  return (
    <div className={`lc-gauge lc-t-${tone}`} role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={v === null ? undefined : Math.round(v)} style={height ? { height } : undefined}>
      <span style={{ width: v === null ? '35%' : `${v}%` }} />
    </div>
  );
}

export function LiteStat({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: Tone }) {
  return <div className={`lc-stat${tone ? ` lc-t-${tone}` : ''}`}><dt>{label}</dt><dd>{value}</dd>{hint && <small>{hint}</small>}</div>;
}
export const LiteStats = ({ children }: { children: ReactNode }) => <dl className="lc-stats">{children}</dl>;

export function LiteTable({ children, label }: { children: ReactNode; label: string }) {
  return <div className="tbl-wrap lc-table-wrap"><table className="tbl lc-table" aria-label={label}>{children}</table></div>;
}

/** États vide / chargement / erreur : un seul rendu, sobre. */
export function LiteState({ kind, children, action }: { kind: 'empty' | 'loading' | 'error'; children: ReactNode; action?: ReactNode }) {
  return (
    <div className={`lc-state lc-state-${kind}`} role={kind === 'error' ? 'alert' : 'status'}>
      {kind === 'error' ? <AlertTriangle size={16} aria-hidden /> : kind === 'loading' ? <span className="lc-spin" aria-hidden /> : <Inbox size={16} aria-hidden />}
      <span>{children}</span>{action}
    </div>
  );
}

/** Pastille de couleur réelle (jamais une couleur inventée : motif neutre quand la couleur est inconnue). */
export function LiteSwatch({ color, size = 22, label }: { color?: string; size?: number; label?: string }) {
  const c = safeColor(color);
  return <span className={`lc-swatch${c ? '' : ' lc-swatch-none'}`} style={{ width: size, height: size, background: c }} role="img" aria-label={label ?? (c ? `couleur ${c}` : 'couleur inconnue')} />;
}

/**
 * Silhouette d'encombrement : boîte isométrique aux proportions réelles (x, y, z) quand elles sont connues. Ce n'est PAS un rendu
 * de la pièce (l'iframe de l'hôte ne peut pas charger l'aperçu) : la teinte vient du nom, les proportions des dimensions.
 */
export function LiteBox({ dims, seed, size = 64, label }: { dims?: { x: number; y: number; z: number }; seed: string; size?: number; label?: string }) {
  const d = dims && dims.x > 0 && dims.y > 0 && dims.z > 0 ? dims : { x: 1, y: 1, z: 1 };
  const m = Math.max(d.x, d.y, d.z);
  const w = (d.x / m) * 20, l = (d.y / m) * 20, h = (d.z / m) * 20;
  const c30 = Math.cos(Math.PI / 6), s30 = 0.5;
  const P = (x: number, y: number, z: number): [number, number] => [(x - y) * c30, (x + y) * s30 - z];
  const pts = (a: Array<[number, number, number]>) => a.map(([x, y, z]) => P(x, y, z).map((n) => n.toFixed(2)).join(',')).join(' ');
  const top = pts([[0, 0, h], [w, 0, h], [w, l, h], [0, l, h]]);
  const left = pts([[0, l, 0], [w, l, 0], [w, l, h], [0, l, h]]);
  const right = pts([[w, 0, 0], [w, l, 0], [w, l, h], [w, 0, h]]);
  return (
    <svg className="lc-box" width={size} height={size} viewBox="-22 -26 44 50" role="img" aria-label={label ?? 'silhouette aux proportions de la pièce'} style={{ '--h': hueOf(seed) } as CSSProperties}>
      <polygon points={left} className="lc-box-l" /><polygon points={right} className="lc-box-r" /><polygon points={top} className="lc-box-t" />
    </svg>
  );
}

export const toneOfState = (s: string): Tone => {
  const x = s.toUpperCase();
  if (['COMPLETED', 'DONE', 'SUCCESS', 'SUCCEEDED', 'OK'].includes(x)) return 'ok';
  if (['FAILED', 'ERROR', 'CANCELLED', 'CANCELED', 'EMPTY'].includes(x)) return 'fail';
  if (['PRINTING', 'RUNNING', 'UPLOADING', 'SLICING', 'START_REQUESTED', 'IDLE'].includes(x)) return x === 'IDLE' ? 'off' : 'run';
  if (['AWAITING_PRINT_CONFIRMATION', 'PARTIAL', 'LOW', 'START_UNKNOWN', 'READY', 'WAITING', 'NEEDS_ASSIGNMENTS', 'PAUSED'].includes(x)) return 'warn';
  return 'off';
};
