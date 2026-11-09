import { createContext, useCallback, useContext, useId, useState } from 'react';
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react';
import { AlertTriangle, ChevronDown, ChevronRight, Inbox, Search, X } from 'lucide-react';
import './components.css';

const cx = (...a: Array<string | false | undefined>) => a.filter(Boolean).join(' ');

/* ---------- Button ---------- */
export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger';
interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> { variant?: ButtonVariant; size?: 'md' | 'sm'; icon?: ReactNode }
export function Button({ variant = 'secondary', size = 'md', icon, children, className, type = 'button', ...p }: ButtonProps) {
  return <button type={type} className={cx('btn', `btn-${variant}`, size === 'sm' && 'btn-sm', className)} {...p}>{icon}{children}</button>;
}
export function IconButton({ label, pressed, children, className, type = 'button', ...p }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string; pressed?: boolean }) {
  return <Tooltip text={label}><button type={type} aria-label={label} aria-pressed={pressed} className={cx('icon-btn', className)} {...p}>{children}</button></Tooltip>;
}

/* ---------- Champs ---------- */
export function Field({ label, children, hint, error }: { label: string; children: ReactNode; hint?: string; error?: string }) {
  return <label className="field"><span>{label}</span>{children}{(error || hint) && <span className={error ? 'fld-msg fld-msg-err' : 'fld-msg'} role={error ? 'alert' : undefined}>{error ?? hint}</span>}</label>;
}
export function Input(p: InputHTMLAttributes<HTMLInputElement>) { return <input className="input" {...p} />; }
export function SearchInput({ value, onChange, placeholder = 'Rechercher', ...p }: Omit<InputHTMLAttributes<HTMLInputElement>, 'onChange' | 'value'> & { value: string; onChange: (v: string) => void }) {
  return (
    <div className="input-wrap">
      <Search size={14} aria-hidden />
      <input className="input" type="search" aria-label={placeholder} placeholder={placeholder} value={value} onChange={(e) => onChange(e.target.value)} {...p} />
    </div>
  );
}
export function Select({ options, ...p }: SelectHTMLAttributes<HTMLSelectElement> & { options: Array<{ value: string; label: string }> }) {
  return <select className="select" {...p}>{options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</select>;
}

/* ---------- Segmented ---------- */
export function Segmented<T extends string>({ value, onChange, options, label, disabled }: { value: T; onChange: (v: T) => void; options: Array<{ value: T; label: string; disabled?: boolean }>; label: string; disabled?: boolean }) {
  return (
    <div className="seg" role="radiogroup" aria-label={label}>
      {options.map((o) => (
        <button key={o.value} type="button" role="radio" aria-checked={value === o.value} disabled={disabled || o.disabled} onClick={() => onChange(o.value)}>{o.label}</button>
      ))}
    </div>
  );
}

/* ---------- Toggle ---------- */
export function Toggle({ checked, onChange, label, disabled }: { checked: boolean; onChange: (v: boolean) => void; label: string; disabled?: boolean }) {
  return (
    <label className="toggle">
      <input type="checkbox" role="switch" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span className="track" aria-hidden />
      <span>{label}</span>
    </label>
  );
}

/* ---------- Slider ---------- */
export function Slider({ label, value, onChange, min, max, step = 1, unit = '', disabled }: { label: string; value: number; onChange: (v: number) => void; min: number; max: number; step?: number; unit?: string; disabled?: boolean }) {
  const id = useId();
  const decimals = (String(step).split('.')[1] ?? '').length;
  return (
    <div className="slider">
      <label htmlFor={id}>{label}</label>
      <output className="val" htmlFor={id}>{value.toFixed(decimals)}{unit && ` ${unit}`}</output>
      <input id={id} type="range" min={min} max={max} step={step} value={value} disabled={disabled} onChange={(e) => onChange(Number(e.target.value))} />
    </div>
  );
}

/* ---------- Tag ---------- */
export function Tag({ tone, children, onClick }: { tone?: 'accent' | 'ok' | 'warn' | 'err'; children: ReactNode; onClick?: () => void }) {
  const c = cx('tag', tone && `tag-${tone}`);
  return onClick ? <button type="button" className={c} onClick={onClick}>{children}</button> : <span className={c}>{children}</span>;
}

/* ---------- StatusDot ---------- */
export type Status = 'online' | 'offline' | 'unavailable' | 'conflict' | 'unused';
const statusLabel: Record<Status, string> = { online: 'En ligne', offline: 'Hors ligne', unavailable: 'Indisponible', conflict: 'Conflit de version', unused: 'Non utilisé' };
export function StatusDot({ status, label }: { status: Status; label?: string }) {
  return <span className={status === 'unused' ? 'status status-unused' : 'status'}><span className={`dot dot-${status}`} aria-hidden />{label ?? statusLabel[status]}</span>;
}

/* ---------- Tooltip ---------- */
export function Tooltip({ text, children }: { text: string; children: ReactNode }) {
  return <span className="tip">{children}<span className="tip-bubble" role="tooltip">{text}</span></span>;
}

/* ---------- Panel ---------- */
export function Panel({ title, actions, children, className, flush }: { title?: string; actions?: ReactNode; children: ReactNode; className?: string; flush?: boolean }) {
  return (
    <section className={cx('panel', className)}>
      {title && <header className="panel-head"><h3>{title}</h3><div>{actions}</div></header>}
      <div className={flush ? undefined : 'panel-body'} style={flush ? { overflow: 'auto', minHeight: 0 } : undefined}>{children}</div>
    </section>
  );
}

/* ---------- Tabs ---------- */
export function Tabs<T extends string>({ value, onChange, tabs, label }: { value: T; onChange: (v: T) => void; tabs: Array<{ value: T; label: string; disabled?: boolean }>; label: string }) {
  return (
    <div className="tabs" role="tablist" aria-label={label}>
      {tabs.map((t) => <button key={t.value} role="tab" type="button" aria-selected={value === t.value} disabled={t.disabled} onClick={() => onChange(t.value)}>{t.label}</button>)}
    </div>
  );
}

/* ---------- Liste ---------- */
export function List<T>({ items, getKey, selectedKey, onSelect, render, label }: { items: T[]; getKey: (i: T) => string; selectedKey?: string; onSelect?: (i: T) => void; render: (i: T) => ReactNode; label: string }) {
  return (
    <ul className="list" role="listbox" aria-label={label}>
      {items.map((i) => (
        <li key={getKey(i)} role="presentation">
          <button type="button" role="option" className="list-item" aria-selected={selectedKey === getKey(i)} onClick={() => onSelect?.(i)}>{render(i)}</button>
        </li>
      ))}
    </ul>
  );
}

/* ---------- Arbre ---------- */
export interface TreeNode { id: string; label: ReactNode; children?: TreeNode[] }
export function Tree({ nodes, selectedId, onSelect, label }: { nodes: TreeNode[]; selectedId?: string; onSelect?: (id: string) => void; label: string }) {
  return <ul className="tree" role="tree" aria-label={label}>{nodes.map((n) => <TreeItem key={n.id} node={n} selectedId={selectedId} onSelect={onSelect} />)}</ul>;
}
function TreeItem({ node, selectedId, onSelect }: { node: TreeNode; selectedId?: string; onSelect?: (id: string) => void }) {
  const [open, setOpen] = useState(true);
  const has = !!node.children?.length;
  return (
    <li role="treeitem" aria-expanded={has ? open : undefined} aria-selected={selectedId === node.id}>
      <button type="button" className="list-item" aria-selected={selectedId === node.id} onClick={() => { onSelect?.(node.id); if (has) setOpen((o) => !o); }}>
        <span className="tree-toggle" aria-hidden>{has ? (open ? <ChevronDown size={14} /> : <ChevronRight size={14} />) : null}</span>
        {node.label}
      </button>
      {has && open && <ul className="tree-children" role="group">{node.children!.map((c) => <TreeItem key={c.id} node={c} selectedId={selectedId} onSelect={onSelect} />)}</ul>}
    </li>
  );
}

/* ---------- Toast ---------- */
interface ToastItem { id: number; tone: 'ok' | 'err' | 'info'; text: string }
const ToastCtx = createContext<(tone: ToastItem['tone'], text: string) => void>(() => {});
export const useToast = () => useContext(ToastCtx);
export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const push = useCallback((tone: ToastItem['tone'], text: string) => {
    const id = Date.now() + Math.random();
    setItems((l) => [...l, { id, tone, text }]);
    window.setTimeout(() => setItems((l) => l.filter((t) => t.id !== id)), 5000);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {items.map((t) => (
          <div key={t.id} className={`toast toast-${t.tone}`}>
            <span style={{ flex: 1 }}>{t.text}</span>
            <IconButton label="Fermer" onClick={() => setItems((l) => l.filter((x) => x.id !== t.id))} style={{ width: 22, height: 22 }}><X size={14} /></IconButton>
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

/* ---------- États vide / chargement / erreur ---------- */
export function EmptyState({ title, text, action }: { title: string; text?: string; action?: ReactNode }) {
  return <div className="state"><Inbox size={18} aria-hidden /><h3>{title}</h3>{text && <p>{text}</p>}{action}</div>;
}
export function LoadingState({ label = 'Chargement' }: { label?: string }) {
  return <div className="state" role="status" aria-busy="true"><span className="spinner" aria-hidden /><h3>{label}</h3><div className="skeleton" style={{ width: 220 }} /><div className="skeleton" style={{ width: 150 }} /></div>;
}
export function ErrorState({ title, text, action }: { title: string; text?: string; action?: ReactNode }) {
  return <div className="state state-err" role="alert"><AlertTriangle size={18} aria-hidden /><h3>{title}</h3>{text && <p>{text}</p>}{action}</div>;
}
