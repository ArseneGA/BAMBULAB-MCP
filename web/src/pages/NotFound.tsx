import { useState } from 'react';
import type { FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Button, EmptyState, PageHeader, SearchInput } from '../components';
import { NAV } from '../nav';

/**
 * Page « Introuvable » (G10 UX1) : remplace la redirection silencieuse vers l'accueil. Statut 200 (repli SPA du serveur),
 * un h1, une recherche (dans la bibliothèque tant que la recherche globale n'existe pas) et les 7 entrées du menu.
 */
export function NotFoundPage() {
  const [q, setQ] = useState('');
  const nav = useNavigate();
  const go = (e: FormEvent) => { e.preventDefault(); if (q.trim()) nav(`/bibliotheque?q=${encodeURIComponent(q.trim())}`); };
  return (
    <>
      <PageHeader title="Page introuvable" help="Cette adresse ne correspond à aucun écran de Printalk : le lien est peut-être ancien ou mal recopié." />
      <div className="stack">
        <form onSubmit={go} role="search" aria-label="Rechercher dans la bibliothèque" className="notfound-search">
          <SearchInput value={q} onChange={setQ} placeholder="Rechercher un modèle" aria-label="Rechercher un modèle" />
          <Button type="submit" variant="primary">Rechercher</Button>
        </form>
        <EmptyState title="Où aller ?" text="Choisissez une des sections ci-dessous, ou recherchez un modèle de la bibliothèque."
          action={<ul className="notfound-links" aria-label="Sections de Printalk">{NAV.map((e) => <li key={e.id}><Link className="btn btn-secondary btn-sm" to={e.path}>{e.label}</Link></li>)}</ul>} />
      </div>
    </>
  );
}
