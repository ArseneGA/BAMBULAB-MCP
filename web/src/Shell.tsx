import { Suspense, useEffect, useState } from 'react';
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom';
import { Moon, Sun, X } from 'lucide-react';
import { StatusDot, ToastProvider } from './components';
import type { Status } from './components';
import { MobileTabBar, NavProgress, RouteFallback, useRouteBusy } from './components';
import { useTheme } from '@shared/theme';
import { ConnectionProvider, useConnection } from './api/hooks';
import { api } from './api/client';
import { EnrollGate, HttpsBanner, ServerBanner } from './pages/common';
import { LOGO_URL, PRODUCT_NAME, TAGLINE, pageTitle } from './brand';
import { NAV, titleFor } from './nav';
import './shell.css';

/** Zone principale : titre d'onglet à chaque changement de route, écrans chargés à la demande avec barre de chargement et squelette (la coque reste affichée). */
function Main() {
  const c = useConnection();
  const { pathname } = useLocation();
  useEffect(() => {
    document.title = pageTitle(titleFor(pathname));
    window.scrollTo(0, 0);
  }, [pathname]);
  return <main className="main" id="contenu" tabIndex={-1} aria-label="Contenu">{c.authRequired ? <EnrollGate /> : <Suspense fallback={<RouteFallback />}><Outlet /></Suspense>}</main>;
}

/** Après chaque navigation (hors premier affichage), le focus va au titre h1 de la page : lecteur d'écran et clavier savent où ils sont. */
function useFocusHeading() {
  const { pathname } = useLocation();
  const [first, setFirst] = useState(true);
  useEffect(() => {
    if (first) { setFirst(false); return; }
    const move = () => {
      const h = document.querySelector<HTMLElement>('#contenu h1');
      if (!h) return false;
      h.setAttribute('tabindex', '-1');
      h.focus({ preventScroll: true });
      return true;
    };
    const t1 = window.setTimeout(() => { if (!move()) window.setTimeout(move, 400); }, 30);
    return () => window.clearTimeout(t1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pathname]);
}

/** Nombre d'impressions qui attendent un geste de l'humain (confirmation), relevé toutes les 15 s quand l'onglet est visible. */
function useTodoCount(): number {
  const c = useConnection();
  const [n, setN] = useState(0);
  useEffect(() => {
    if (c.server !== 'online' || c.authRequired) return;
    let alive = true;
    const run = () => {
      if (document.visibilityState === 'hidden') return;
      api.printJobs(undefined, 'all').then((r) => { if (alive) setN(r.print_jobs.filter((j) => ['UPLOADED', 'AWAITING_PRINT_CONFIRMATION'].includes(j.state)).length); }, () => undefined);
    };
    run();
    const t = window.setInterval(run, 15000);
    document.addEventListener('visibilitychange', run);
    return () => { alive = false; window.clearInterval(t); document.removeEventListener('visibilitychange', run); };
  }, [c.server, c.authRequired]);
  return n;
}

/** Une seule ligne d'état : « Tout fonctionne » ou « 1 problème » (le détail est dans Réglages › Avancé). */
export function ServiceLine() {
  const c = useConnection();
  const problems = (c.server === 'offline' ? 1 : 0) + (c.agents.length > 0 && c.windows === 'offline' ? 1 : 0);
  const st: Status = c.server === 'checking' ? 'unavailable' : problems === 0 ? 'online' : 'offline';
  const text = c.server === 'checking' ? 'Connexion…' : problems === 0 ? 'Tout fonctionne' : `${problems} problème${problems > 1 ? 's' : ''}`;
  return (
    <div className="side-status" aria-label="État du service">
      <Link to="/reglages/avance" title="Voir le détail de l'état du service"><StatusDot status={st} label={text} /></Link>
      {c.browserEnrolled === false && <a className="enroll" href="/enroll" title="Les pages de confirmation d'impression exigent un navigateur enregistré (code à usage unique)">Enregistrer ce navigateur</a>}
    </div>
  );
}

function ThemeButton() {
  const { theme, toggle } = useTheme();
  return (
    <button type="button" className="theme" onClick={toggle} title={theme === 'light' ? 'Thème sombre' : 'Thème clair'} aria-label={theme === 'light' ? 'Passer au thème sombre' : 'Passer au thème clair'}>
      {theme === 'light' ? <Moon aria-hidden /> : <Sun aria-hidden />}
    </button>
  );
}

function Sidebar({ badges }: { badges: Record<string, number> }) {
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
        {NAV.map((e) => {
          const I = e.icon;
          const n = e.id === 'bibliotheque' ? count : badges[e.id];
          return (
            <NavLink key={e.id} to={e.path} end={e.path === '/'}>
              <I aria-hidden /><span className="t">{e.label}</span>
              {n !== undefined && n > 0 && <span className={`n${e.id === 'bibliotheque' ? '' : ' n-todo'}`} aria-label={e.id === 'bibliotheque' ? `${n} modèles` : `${n} à traiter`}>{n}</span>}
            </NavLink>
          );
        })}
      </nav>
      <div className="side-foot">
        <ServiceLine />
        <div className="side-row"><div className="who">Réseau local</div><ThemeButton /></div>
      </div>
    </aside>
  );
}

const MENU_KEY = 'printalk.menu-v2-vu';
/** Bandeau fermable, mémorisé dans le navigateur : l'ancien menu a été réorganisé (anciens liens toujours valides). */
export function MenuChangedBanner() {
  const [shown, setShown] = useState(() => { try { return localStorage.getItem(MENU_KEY) !== '1'; } catch { return true; } });
  if (!shown) return null;
  const close = () => { setShown(false); try { localStorage.setItem(MENU_KEY, '1'); } catch { /* stockage indisponible */ } };
  return (
    <div className="menu-banner" role="region" aria-label="Le menu a changé">
      <p><strong>Le menu a changé :</strong> les coûts sont dans <Link to="/impressions?vue=bilan">Impressions › Bilan</Link>, les bobines et matériaux dans <Link to="/matieres">Matières</Link>, la corbeille et les gabarits dans <Link to="/bibliotheque">Bibliothèque</Link>, « Préparer » dans la fiche d'un modèle. Les anciens liens fonctionnent toujours.</p>
      <button type="button" className="icon-btn" aria-label="Fermer ce message" onClick={close}><X size={16} aria-hidden /></button>
    </div>
  );
}

function Frame() {
  const c = useConnection();
  const busy = useRouteBusy();
  const todo = useTodoCount();
  useFocusHeading();
  const badges = { accueil: todo, impressions: todo };
  return (
    <>
      <NavProgress active={busy} />
      <div className="halos" aria-hidden><div className="halo a" /><div className="halo b" /><div className="halo c" /><div className="halo d" /></div>
      <a className="skip-link" href="#contenu">Aller au contenu</a>
      <div className="app">
        <Sidebar badges={badges} />
        <div className="app-body">
          <header className="topbar">
            <Link to="/" className="brand" aria-label={`${PRODUCT_NAME}, accueil`}><img className="brand-logo" src={LOGO_URL} alt="" width={28} height={28} /><b>{PRODUCT_NAME}</b></Link>
          </header>
          <div className="banners"><ServerBanner /><HttpsBanner />{c.server === 'online' && <MenuChangedBanner />}</div>
          <Main />
        </div>
      </div>
      <MobileTabBar badges={badges} sheetExtra={<div className="sheet-foot"><ServiceLine /><ThemeButton /></div>} />
    </>
  );
}

export function Shell() {
  return (
    <ToastProvider>
      <ConnectionProvider>
        <Frame />
      </ConnectionProvider>
    </ToastProvider>
  );
}
