import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { Ellipsis, X } from 'lucide-react';
import { NAV } from '../nav';
import type { NavEntry } from '../nav';

const isActive = (e: NavEntry, pathname: string) => (e.path === '/' ? pathname === '/' : pathname === e.path || pathname.startsWith(e.path + '/'));

/**
 * Barre basse mobile (< 761 px, G10 UX3) : 4 entrées + « Plus » (feuille modale), icône ET libellé toujours visibles,
 * zones de 56 px de haut (≥ 44 × 44). Les entrées « more » de nav.ts vivent dans la feuille.
 */
export function MobileTabBar({ badges = {}, sheetExtra }: { badges?: Record<string, number>; sheetExtra?: ReactNode }) {
  const [open, setOpen] = useState(false);
  const { pathname } = useLocation();
  const trigger = useRef<HTMLButtonElement>(null);
  const dlg = useRef<HTMLDialogElement>(null);
  const bar = NAV.filter((e) => e.mobile === 'bar');
  const more = NAV.filter((e) => e.mobile === 'more');
  const moreActive = more.some((e) => isActive(e, pathname));

  useEffect(() => {
    const d = dlg.current;
    if (!d) return;
    if (open && !d.open) { if (typeof d.showModal === 'function') d.showModal(); else d.setAttribute('open', ''); }
    if (!open && d.open) { if (typeof d.close === 'function') d.close(); else d.removeAttribute('open'); }
  }, [open]);
  useEffect(() => { setOpen(false); }, [pathname]);

  const close = () => { setOpen(false); trigger.current?.focus(); };

  return (
    <>
      <nav className="tabbar" aria-label="Navigation principale">
        {bar.map((e) => {
          const I = e.icon;
          const n = badges[e.id];
          return (
            <NavLink key={e.id} to={e.path} end={e.path === '/'} className={isActive(e, pathname) ? 'active' : undefined} aria-current={isActive(e, pathname) ? 'page' : undefined}>
              <span className="tb-ico"><I aria-hidden />{n ? <span className="tb-n" aria-label={`${n} à traiter`}>{n}</span> : null}</span>
              <span className="tb-t">{e.label}</span>
            </NavLink>
          );
        })}
        <button ref={trigger} type="button" className={moreActive ? 'active' : undefined} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}>
          <span className="tb-ico"><Ellipsis aria-hidden /></span><span className="tb-t">Plus</span>
        </button>
      </nav>
      <dialog ref={dlg} className="sheet" aria-label="Plus" onClose={() => { if (open) close(); }} onCancel={(e) => { e.preventDefault(); close(); }} onClick={(e) => { if (e.target === dlg.current) close(); }}>
        {open && (
          <div className="sheet-box">
            <header className="sheet-head"><h2>Plus</h2><button type="button" className="icon-btn" aria-label="Fermer" onClick={close}><X size={16} aria-hidden /></button></header>
            <ul className="sheet-list">
              {more.map((e) => {
                const I = e.icon;
                return <li key={e.id}><NavLink to={e.path} onClick={() => setOpen(false)}><I aria-hidden /><span>{e.label}</span></NavLink></li>;
              })}
            </ul>
            {sheetExtra}
          </div>
        )}
      </dialog>
    </>
  );
}
