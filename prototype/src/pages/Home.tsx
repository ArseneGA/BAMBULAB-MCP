import { Link } from 'react-router-dom';
import { ArrowUpRight } from 'lucide-react';
import './home.css';

const STEPS = [
  { to: '/bibliotheque', title: 'Retrouver ou importer', text: 'Recherche, collections, filtres de format et de disponibilité des fichiers.' },
  { to: '/modele/m01', title: 'Inspecter', text: 'Viewer 3D, sous-parties, fichiers et versions du modèle.' },
  { to: '/preparation', title: 'Préciser le résultat', text: 'Réglages par objet ou zone, objectif de finition, copie de travail.' },
  { to: '/comparaison', title: 'Comparer', text: 'Trois variantes côte à côte, caméra partagée, application sans écraser une révision concurrente.' },
  { to: '/materiaux', title: 'Choisir la matière', text: 'Fiches matériaux et critères issus de l\'objectif ; l\'inconnu reste inconnu.' },
  { to: '/impression', title: 'Préparer, envoyer, confirmer', text: 'Validation, transfert, confirmation explicite du lancement et suivi.' },
] as const;

export function Home() {
  return (
    <>
      <div className="page-head">
        <span className="eyebrow">Prototype D0</span>
        <h1>Atelier</h1>
        <p>Parcours de démonstration, du fichier retrouvé à l'impression confirmée. Données fictives : aucune action ne contacte un serveur, Bambu Studio ou la P1S.</p>
      </div>
      <ol className="home-steps" aria-label="Parcours">
        {STEPS.map((s, i) => (
          <li key={s.to}>
            <Link to={s.to} className="home-step">
              <span className="home-n mono" aria-hidden>{i + 1}</span>
              <span className="home-t"><strong>{s.title}</strong><span>{s.text}</span></span>
              <ArrowUpRight size={15} aria-hidden />
            </Link>
          </li>
        ))}
      </ol>
      <p className="home-more">
        <Link to="/claude">Vue compacte dans une conversation</Link><span aria-hidden>·</span><Link to="/design">Système de design</Link>
      </p>
    </>
  );
}
