import { Link } from 'react-router-dom';
import { ArrowUpRight } from 'lucide-react';
import { StatusDot, Tag } from '../components';
import { api } from '../api/client';
import { useAsync, useConnection } from '../api/hooks';
import { Async, FormatBadge, fmtBytes, fmtDate } from './common';
import './home.css';

const STEPS = [
  { to: '/bibliotheque', title: 'Retrouver ou importer', text: 'Recherche, collections, filtres de format et de disponibilité, import non destructif.', live: true },
  { to: '/bibliotheque', title: 'Inspecter', text: 'Viewer 3D du vrai fichier, sous-parties, analyse géométrique, fichiers et versions.', live: true },
  { to: '/preparation', title: 'Préciser le résultat', text: 'Objectif de finition traduit en critères validés par le serveur : ambiguïtés, compromis, critères à vérifier par essai.', live: true },
  { to: '/comparaison', title: 'Comparer', text: 'Variantes découpées par Bambu Studio, mesures étiquetées, application refusée si la révision a bougé.', live: true },
  { to: '/materiaux', title: 'Machines et matériaux', text: "Modèles d'imprimantes Bambu connus et parc enregistré ; l'inconnu reste inconnu.", live: true },
  { to: '/impression', title: 'Préparer, envoyer, confirmer', text: "Découpe, validation, dépôt sur l'imprimante, confirmation explicite sur une page dédiée, suivi.", live: true },
] as const;

export function Home() {
  const c = useConnection();
  const recent = useAsync((s) => api.models({ sort: 'updated', desc: true, limit: 5 }, s), []);
  return (
    <>
      <div className="page-head">
        <span className="eyebrow">Atelier Bambu Lab</span>
        <h1>Atelier</h1>
        <p>Catalogue 3D branché sur le service bambud. Préparation, comparaison, machines et impression sont branchées sur le serveur ; les fiches matériaux ne sont pas encore publiées.</p>
      </div>

      <div className="home-grid">
        <div>
          <ol className="home-steps" aria-label="Parcours">
            {STEPS.map((s, i) => (
              <li key={s.title}>
                <Link to={s.to} className="home-step">
                  <span className="home-n mono" aria-hidden>{i + 1}</span>
                  <span className="home-t"><strong>{s.title} {!s.live && <Tag tone="warn">bientôt</Tag>}</strong><span>{s.text}</span></span>
                  <ArrowUpRight size={15} aria-hidden />
                </Link>
              </li>
            ))}
          </ol>
        </div>
        <aside className="home-side" aria-label="État du service">
          <section className="panel"><header className="panel-head"><h3>Service</h3></header>
            <div className="panel-body home-status">
              <StatusDot status={c.server === 'online' ? 'online' : c.server === 'checking' ? 'unavailable' : 'offline'} label={c.server === 'online' ? `bambud ${c.health?.version ?? ''}` : c.server === 'checking' ? 'Connexion…' : 'bambud injoignable'} />
              <StatusDot status={c.windows === 'online' ? 'online' : 'offline'} label={c.windows === 'online' ? 'Agent Windows en ligne' : 'Agent Windows hors ligne'} />
              <StatusDot status={c.cad === 'online' ? 'online' : 'unavailable'} label={c.cad === 'online' ? 'Backend CAO disponible' : 'Backend CAO absent (STEP)'} />
              {c.cad !== 'online' && c.cadNote && <p className="hint" style={{ margin: 0 }}>{c.cadNote}</p>}
              {c.server === 'online' && !c.importEnabled && <p className="hint" style={{ margin: 0 }}>{c.importNote}</p>}
            </div></section>
          <section className="panel"><header className="panel-head"><h3>Récemment modifiés</h3></header>
            <div className="panel-body" style={{ padding: 0 }}>
              <Async state={recent} label="Chargement" empty={(d) => d.models.length === 0 ? <p className="hint" style={{ padding: 'var(--sp-3)', margin: 0 }}>Le catalogue est vide. <Link to="/bibliotheque">Importer des modèles</Link>.</p> : null}>
                {(d) => (
                  <ul className="home-recent">
                    {d.models.map((m) => (
                      <li key={m.id}><Link to={`/modele/${m.id}`}><FormatBadge formats={m.formats} size={32} /><span><strong>{m.name}</strong><span className="sub">{m.collection_path ?? '—'} · {fmtBytes(m.size_bytes)} · {fmtDate(m.updated_at)}</span></span></Link></li>
                    ))}
                  </ul>
                )}
              </Async>
            </div></section>
        </aside>
      </div>
    </>
  );
}
