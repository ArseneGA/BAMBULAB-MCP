import { NavLink } from 'react-router-dom';

/** Bascule de l'écran « Préparer » : l'objectif de finition, puis la comparaison des variantes découpées. */
export function PrepareNav() {
  return (
    <nav className="prep-nav" aria-label="Préparer">
      <div className="seg">
        <NavLink to="/preparation" end>Objectif et préparation</NavLink>
        <NavLink to="/comparaison">Comparer les variantes</NavLink>
      </div>
    </nav>
  );
}
