import { Link, useLocation } from 'react-router-dom';
import { crumbsFor } from '../nav';
import type { Crumb } from '../nav';

/**
 * Fil d'Ariane partagé (G10 UX1) : déduit de nav.ts, ou fourni par la page quand elle connaît des noms propres
 * (« Bibliothèque › Atelier › Support »). Visible dès la profondeur 2 ; le dernier élément n'est pas un lien (aria-current="page").
 * Sous 761 px, la liste est remplacée par un seul lien « ← Parent » (voir shell.css).
 */
export function Breadcrumb({ items }: { items?: Crumb[] }) {
  const { pathname } = useLocation();
  const crumbs = items ?? crumbsFor(pathname);
  if (crumbs.length < 2) return null;
  const parent = [...crumbs].slice(0, -1).reverse().find((c) => c.to);
  return (
    <nav className="crumb" aria-label="Fil d'Ariane">
      <ol>
        {crumbs.map((c, i) => {
          const last = i === crumbs.length - 1;
          return (
            <li key={`${i}-${c.label}`}>
              {last ? <span aria-current="page">{c.label}</span> : c.to ? <Link to={c.to}>{c.label}</Link> : <span>{c.label}</span>}
              {!last && <span className="crumb-sep" aria-hidden>›</span>}
            </li>
          );
        })}
      </ol>
      {parent?.to && <Link className="crumb-back" to={parent.to}><span aria-hidden>←</span> {parent.label}</Link>}
    </nav>
  );
}
