/**
 * Identité du produit : SEULE source du nom et de la baseline (nom provisoire).
 * Utilisée par l'interface (barre latérale, titres, accueil) et par vite.config.ts pour <title> et la description.
 * Pour renommer le produit, modifier ces deux constantes puis reconstruire.
 */
export const PRODUCT_NAME = 'Printalk';
export const TAGLINE = 'Fabriquer en parlant';

/** Icône du produit (copie de docs/logo/atelier-icon.svg, servie depuis web/public). */
export const LOGO_URL = `${import.meta.env?.BASE_URL ?? '/'}atelier-icon.svg`;

/** Titre d'onglet : « Printalk » ou « Bibliothèque · Printalk ». */
export const pageTitle = (section?: string) => (section ? `${section} · ${PRODUCT_NAME}` : PRODUCT_NAME);
