import { NavLink } from 'react-router-dom';

/** Navigation entre les sous-pages d'une section (Réglages, Bibliothèque) : des liens, pas des onglets (chaque sous-page a son URL). */
export function SectionNav({ label, items }: { label: string; items: Array<{ to: string; label: string; end?: boolean }> }) {
  return (
    <nav className="section-nav" aria-label={label}>
      {items.map((i) => <NavLink key={i.to} to={i.to} end={i.end ?? true}>{i.label}</NavLink>)}
    </nav>
  );
}
