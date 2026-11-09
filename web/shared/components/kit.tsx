import { useEffect, useId, useState } from 'react';
import type { InputHTMLAttributes, ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, Info } from 'lucide-react';
import './kit.css';

/**
 * Pièces du socle de design Printalk, communes à l'interface web (web/src) et aux cartes affichées dans Claude
 * (web/mcp-app). Tout est stylé avec les jetons de tokens.css : clair, sombre, 34 px de contrôle, vert Printalk.
 * Les composants plus anciens (Button, Input, Select, Segmented, Toggle, Slider, Tag, StatusDot, Panel, Toast,
 * EmptyState…) sont dans ui.tsx ; ce fichier ajoute ce qu'il fallait pour des cartes autonomes et des formulaires.
 */

const cx = (...a: Array<string | false | undefined | null>) => a.filter(Boolean).join(' ');

/* ---------- Card : conteneur autonome (carte MCP App, bloc de page) ---------- */
export type Tone = 'neutral' | 'run' | 'ok' | 'warn' | 'err';
export function Card({ title, icon, meta, footer, tone, className, children, ...rest }: {
  title?: ReactNode; icon?: ReactNode; meta?: ReactNode; footer?: ReactNode; tone?: Exclude<Tone, 'run'>; className?: string; children?: ReactNode;
} & Omit<React.HTMLAttributes<HTMLElement>, 'title'>) {
  return (
    <section className={cx('card', tone && tone !== 'neutral' && `card-${tone}`, className)} {...rest}>
      {(title || icon || meta) && (
        <header className="card-head">
          {icon && <span className="card-icon" aria-hidden>{icon}</span>}
          {title && <h2 className="card-title">{title}</h2>}
          {meta && <div className="card-meta">{meta}</div>}
        </header>
      )}
      {children !== undefined && children !== null && <div className="card-body">{children}</div>}
      {footer && <footer className="card-foot">{footer}</footer>}
    </section>
  );
}

/* ---------- Badge d'état : couleur ET forme (point), jamais la couleur seule ---------- */
export function Badge({ tone = 'neutral', dot = true, children, className, title }: { tone?: Tone; dot?: boolean; children: ReactNode; className?: string; title?: string }) {
  return <span className={cx('badge', `badge-${tone}`, className)} title={title}>{dot && <i aria-hidden />}{children}</span>;
}

/* ---------- Champ numérique avec unité ---------- */
export interface NumberFieldProps {
  label: string;
  value: number | undefined;
  onChange: (v: number | undefined) => void;
  unit?: string;
  min?: number;
  max?: number;
  step?: number | 'any';
  hint?: string;
  error?: string;
  required?: boolean;
  placeholder?: string;
  disabled?: boolean;
  name?: string;
}
/** Zone vide = undefined (jamais 0 par défaut : une valeur non saisie n'est pas un zéro). Accepte la virgule décimale. */
function parseNumber(raw: string): number | undefined {
  const t = raw.trim().replace(',', '.');
  if (t === '') return undefined;
  const n = Number(t);
  return Number.isFinite(n) ? n : undefined;
}
export function NumberField({ label, value, onChange, unit, min, max, step = 'any', hint, error, required, placeholder, disabled, name }: NumberFieldProps) {
  const id = useId();
  // Le texte saisi est gardé tel quel (« 0, » pendant la frappe) ; il suit la valeur quand elle change de l'extérieur.
  const [raw, setRaw] = useState(value === undefined ? '' : String(value));
  useEffect(() => { setRaw((r) => (parseNumber(r) === value ? r : value === undefined ? '' : String(value))); }, [value]);
  return (
    <div className={cx('fld', error && 'fld-err')}>
      <label htmlFor={id} className="fld-label">{label}{required && <span className="fld-req" aria-hidden> *</span>}</label>
      <div className="num-wrap">
        <input id={id} name={name} className="input num" type="text" inputMode="decimal" data-min={min} data-max={max} data-step={step} value={raw} placeholder={placeholder}
          disabled={disabled} required={required} aria-invalid={error ? true : undefined} aria-describedby={error || hint ? `${id}-d` : undefined}
          onChange={(e) => { setRaw(e.target.value); onChange(parseNumber(e.target.value)); }} />
        {unit && <span className="num-unit" aria-hidden>{unit}</span>}
      </div>
      {(error || hint) && <span id={`${id}-d`} className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}
    </div>
  );
}

/* ---------- Champ texte avec aide et erreur ---------- */
export function TextField({ label, hint, error, required, className, ...p }: { label: string; hint?: string; error?: string; required?: boolean } & InputHTMLAttributes<HTMLInputElement>) {
  const id = useId();
  return (
    <div className={cx('fld', error && 'fld-err')}>
      <label htmlFor={id} className="fld-label">{label}{required && <span className="fld-req" aria-hidden> *</span>}</label>
      <input id={id} className={cx('input', className)} required={required} aria-invalid={error ? true : undefined} aria-describedby={error || hint ? `${id}-d` : undefined} {...p} />
      {(error || hint) && <span id={`${id}-d`} className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}
    </div>
  );
}

/* ---------- Liste déroulante avec libellé, aide et erreur ---------- */
export function SelectField({ label, value, onChange, options, hint, error, required, placeholder, disabled }: {
  label: string; value: string; onChange: (v: string) => void; options: Array<{ value: string; label: string }>;
  hint?: string; error?: string; required?: boolean; placeholder?: string; disabled?: boolean;
}) {
  const id = useId();
  return (
    <div className={cx('fld', error && 'fld-err')}>
      <label htmlFor={id} className="fld-label">{label}{required && <span className="fld-req" aria-hidden> *</span>}</label>
      <select id={id} className="select" value={value} disabled={disabled} required={required} aria-invalid={error ? true : undefined} aria-describedby={error || hint ? `${id}-d` : undefined} onChange={(e) => onChange(e.target.value)}>
        {placeholder !== undefined && <option value="">{placeholder}</option>}
        {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
      {(error || hint) && <span id={`${id}-d`} className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}
    </div>
  );
}

/* ---------- Couleur : pastille + code #RRGGBB ---------- */
export const HEX_RE = /^#[0-9a-fA-F]{6}$/;
export function ColorField({ label, value, onChange, hint, error, presets }: { label: string; value: string; onChange: (v: string) => void; hint?: string; error?: string; presets?: string[] }) {
  const id = useId();
  const ok = HEX_RE.test(value);
  return (
    <div className={cx('fld', error && 'fld-err')}>
      <label htmlFor={id} className="fld-label">{label}</label>
      <div className="color-wrap">
        <span className="swatch-lg" style={{ background: ok ? value : 'transparent' }} role="img" aria-label={ok ? `Couleur ${value}` : 'Couleur non définie'} />
        <input id={id} className="input mono" value={value} maxLength={7} placeholder="#RRGGBB" spellCheck={false} aria-invalid={error ? true : undefined} onChange={(e) => onChange(e.target.value.startsWith('#') || e.target.value === '' ? e.target.value : `#${e.target.value}`)} />
        {presets && presets.length > 0 && (
          <span className="color-presets" role="group" aria-label="Couleurs usuelles">
            {presets.map((c) => <button key={c} type="button" className="swatch-btn" style={{ background: c }} aria-label={`Choisir ${c}`} aria-pressed={value.toLowerCase() === c.toLowerCase()} onClick={() => onChange(c)} />)}
          </span>
        )}
      </div>
      {(error || hint) && <span className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}
    </div>
  );
}

/* ---------- Cases à cocher groupées ---------- */
export function CheckGroup({ label, options, values, onChange, hint }: { label: string; options: Array<{ value: string; label: string; hint?: string }>; values: string[]; onChange: (v: string[]) => void; hint?: string }) {
  return (
    <fieldset className="fld chk-group">
      <legend className="fld-label">{label}</legend>
      <div className="chk-list">
        {options.map((o) => {
          const on = values.includes(o.value);
          return (
            <label key={o.value} className="chk" title={o.hint}>
              <input type="checkbox" checked={on} onChange={() => onChange(on ? values.filter((v) => v !== o.value) : [...values, o.value])} />
              <span>{o.label}</span>
            </label>
          );
        })}
      </div>
      {hint && <span className="fld-msg">{hint}</span>}
    </fieldset>
  );
}

/* ---------- Tableau ---------- */
export interface Column<T> { key: string; header: string; align?: 'left' | 'right'; mono?: boolean; render?: (row: T) => ReactNode }
export function Table<T>({ columns, rows, getKey, caption, empty }: { columns: Array<Column<T>>; rows: T[]; getKey: (r: T) => string; caption?: string; empty?: ReactNode }) {
  if (rows.length === 0 && empty) return <>{empty}</>;
  return (
    <div className="tbl-wrap">
      <table className="tbl">
        {caption && <caption className="sr-only">{caption}</caption>}
        <thead><tr>{columns.map((c) => <th key={c.key} scope="col" className={cx(c.align === 'right' && 'r')}>{c.header}</th>)}</tr></thead>
        <tbody>
          {rows.map((r) => (
            <tr key={getKey(r)}>{columns.map((c) => <td key={c.key} className={cx(c.align === 'right' && 'r', c.mono && 'mono')}>{c.render ? c.render(r) : String((r as Record<string, unknown>)[c.key] ?? '')}</td>)}</tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/* ---------- Bandeau d'information ---------- */
export function Notice({ tone = 'neutral', title, children, action }: { tone?: 'neutral' | 'ok' | 'warn' | 'err'; title?: string; children?: ReactNode; action?: ReactNode }) {
  const Icon = tone === 'ok' ? CheckCircle2 : tone === 'neutral' ? Info : AlertTriangle;
  return (
    <div className={cx('notice', tone === 'warn' && 'notice-warn', tone === 'err' && 'notice-err', tone === 'ok' && 'notice-ok')} role={tone === 'err' ? 'alert' : 'status'}>
      <Icon size={16} aria-hidden />
      <div>{title && <h3>{title}</h3>}{children && <p>{children}</p>}{action && <div className="acts">{action}</div>}</div>
    </div>
  );
}
