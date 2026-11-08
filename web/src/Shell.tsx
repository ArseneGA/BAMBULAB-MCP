import { useEffect, useState } from 'react';
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom';
import { House, Boxes, SlidersHorizontal, Printer, Wifi, Disc3, Coins, Shapes, Layers, Trash2, Settings, Moon, Sun } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { StatusDot, ToastProvider } from './components';
import type { Status } from './components';
import { useTheme } from './design/theme';
import { ConnectionProvider, useConnection } from './api/hooks';
import { api } from './api/client';
import { EnrollGate, HttpsBanner, ServerBanner } from './pages/common';
import { LOGO_URL, PRODUCT_NAME, TAGLINE, pageTitle } from './brand';
import './shell.css';

interface NavItem { path: string; label: string; icon: LucideIcon; end?: boolean; count?: boolean }

/** Navigation principale (maquette) puis « Atelier » (machines, matière, coûts) puis outils annexes. */
export const mainNav: NavItem[] = [
  { path: '', label: 'Accueil', icon: House, end: true },
  { path: 'bibliotheque', label: 'Bibliothèque', icon: Boxes, count: true },
  { path: 'preparation', label: 'Préparer', icon: SlidersHorizontal },
  { path: 'impressions', label: 'Impressions', icon: Printer },
];
export const workshopNav: NavItem[] = [
  { path: 'imprimantes', label: 'Imprimantes', icon: Wifi },
  { path: 'bobines', label: 'Bobines', icon: Disc3 },
  { path: 'couts', label: 'Coûts', icon: Coins },
];
export const moreNav: NavItem[] = [
  { path: 'gabarits', label: 'Gabarits', icon: Shapes },
  { path: 'materiaux', label: 'Matériaux', icon: Layers },
  { path: 'corbeille', label: 'Corbeille', icon: Trash2 },
  { path: 'reglages', label: 'Réglages', icon: Settings },
];
/** Titre de l'onglet selon l'écran courant (les écrans non listés gardent le nom du produit). */
const TITLES: Array<[string, string]> = [
  ['/bibliotheque', 'Bibliothèque'], ['/modele', 'Fiche modèle'], ['/preparation', 'Préparer'], ['/comparaison', 'Variantes'], ['/impressions', 'Impressions'],
  ['/imprimantes', 'Imprimantes'], ['/bobines', 'Bobines'], ['/file', 'File d\'attente'], ['/couts', 'Coûts'], ['/gabarits', 'Gabarits'], ['/materiaux', 'Matériaux'],
  ['/corbeille', 'Corbeille'], ['/reglages', 'Réglages'], ['/agents', 'Agent Windows'], ['/design', 'Système de design'],
];

function SideLink({ item, count }: { item: NavItem; count?: number }) {
  const I = item.icon;
  return (
    <NavLink to={`/${item.path}`} end={item.end}>
      <I aria-hidden /><span className="t">{item.label}</span>{item.count && count !== undefined && <span className="n" aria-label={`${count} modèles`}>{count}</span>}
    </NavLink>
  );
}

function Status3() {
  const c = useConnection();
  const server: Status = c.server === 'online' ? 'online' : c.server === 'checking' ? 'unavailable' : 'offline';
  const win: Status = c.windows === 'online' ? 'online' : c.windows === 'unused' ? 'unused' : c.windows === 'unavailable' ? 'unavailable' : 'offline';
  const cad: Status = c.cad === 'online' ? 'online' : 'unavailable';
  return (
    <div className="side-status" aria-label="État des services">
      <span title={c.server === 'online' ? `Service bambud ${c.health?.version ?? ''}` : 'Service bambud'}><StatusDot status={server} label={c.server === 'checking' ? 'Connexion…' : c.server === 'online' ? 'Service en ligne' : 'Service injoignable'} /></span>
      <Link to="/agents" title={c.agents.length === 0 ? 'Windows : non utilisé (aucun agent appairé ; le mode Linux seul est normal)' : c.agents.map((a) => `${a.name} : ${a.online ? 'en ligne' : 'hors ligne'}${a.studio_running ? ', Bambu Studio ouvert' : a.studio_installed ? ', Bambu Studio installé' : ''}`).join(' ; ')}>
        <StatusDot status={win} label={c.agents.length === 0 ? (win === 'unused' ? 'Windows : non utilisé' : 'Windows') : c.agents.length === 1 ? `Windows : ${c.agents[0].name}` : `Windows ×${c.agents.length}`} />
      </Link>
      <span title={c.cad === 'online' ? 'Backend CAO (STEP) disponible' : (c.cadNote ?? 'Backend CAO absent : les fichiers STEP ne sont pas convertis')}><StatusDot status={cad} label="CAO (STEP)" /></span>
      {c.browserEnrolled === false && <a className="enroll" href="/enroll" title="Les pages de confirmation d'impression exigent un navigateur enregistré (code à usage unique)">Enregistrer ce navigateur</a>}
    </div>
  );
}

/** Sans session de navigateur ni jeton, l'API répond 401 : l'écran d'enregistrement remplace les pages. */
function Main() {
  const c = useConnection();
  const { pathname } = useLocation();
  useEffect(() => {
    const hit = TITLES.find(([p]) => pathname === p || pathname.startsWith(p + '/'));
    document.title = pageTitle(hit?.[1]);
    window.scrollTo(0, 0);
  }, [pathname]);
  return <main className="main" id="contenu" tabIndex={-1}>{c.authRequired ? <EnrollGate /> : <Outlet />}</main>;
}

function Sidebar() {
  const { theme, toggle } = useTheme();
  const c = useConnection();
  const [count, setCount] = useState<number>();
  useEffect(() => {
    if (c.server !== 'online' || c.authRequired) return;
    const ac = new AbortController();
    api.models({ limit: 1 }, ac.signal).then((r) => setCount(r.total), () => undefined);
    return () => ac.abort();
  }, [c.server, c.authRequired]);
  return (
    <aside className="side glass" aria-label="Barre latérale">
      <Link to="/" className="brand" aria-label={`${PRODUCT_NAME}, accueil`}>
        <img className="brand-logo" src={LOGO_URL} alt="" width={36} height={36} />
        <div><b>{PRODUCT_NAME}</b><small>{TAGLINE}</small></div>
      </Link>
      <nav className="nav" aria-label="Navigation principale">
        {mainNav.map((i) => <SideLink key={i.path} item={i} count={count} />)}
        <div className="sep" />
        <div className="nav-label">Atelier</div>
        {workshopNav.map((i) => <SideLink key={i.path} item={i} />)}
        <div className="sep" />
        <div className="nav-label">Plus</div>
        {moreNav.map((i) => <SideLink key={i.path} item={i} />)}
      </nav>
      <div className="side-foot">
        <Status3 />
        <div className="side-row">
          <div className="who">Réseau local<small>{c.health?.version ? `bambud ${c.health.version}` : 'bambud'}</small></div>
          <button type="button" className="theme" onClick={toggle} title={theme === 'light' ? 'Thème sombre' : 'Thème clair'} aria-label={theme === 'light' ? 'Passer au thème sombre' : 'Passer au thème clair'}>
            {theme === 'light' ? <Moon aria-hidden /> : <Sun aria-hidden />}
          </button>
        </div>
      </div>
    </aside>
  );
}

export function Shell() {
  return (
    <ToastProvider>
      <ConnectionProvider>
        <div className="halos" aria-hidden><div className="halo a" /><div className="halo b" /><div className="halo c" /><div className="halo d" /></div>
        <a className="skip-link" href="#contenu">Aller au contenu</a>
        <div className="app">
          <Sidebar />
          <div className="app-body">
            <div className="banners"><ServerBanner /><HttpsBanner /></div>
            <Main />
          </div>
        </div>
      </ConnectionProvider>
    </ToastProvider>
  );
}
