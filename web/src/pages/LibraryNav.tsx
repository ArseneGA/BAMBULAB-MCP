import { SectionNav } from '../components';

/** Sous-vues de la Bibliothèque : les modèles, les pièces sur mesure (gabarits), la corbeille et les doublons. */
export function LibraryNav() {
  return (
    <SectionNav label="Sous-vues de la bibliothèque" items={[
      { to: '/bibliotheque', label: 'Modèles' },
      { to: '/bibliotheque/gabarits', label: 'Pièces sur mesure' },
      { to: '/bibliotheque/corbeille', label: 'Corbeille et doublons' },
    ]} />
  );
}
