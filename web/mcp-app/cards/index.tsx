import type { CardApp, CardProps } from './call';
import { CompareCard, normalizeCompare } from './compare';
import { CostsCard, normalizeCosts, type CostsData } from './costs';
import { IssuesCard, normalizeIssues, type IssuesData } from './issues';
import { LibraryCard, normalizeLibrary, type LibraryData } from './library';
import { PartCard, normalizePart, type PartData } from './part';
import { PrintSheetCard, normalizePrintSheet, type PrintSheetData } from './print-sheet';
import { PrintersCard, normalizePrinters, type PrintersData } from './printers';
import { PrintsCard, normalizePrints, type PrintsData } from './prints';
import { ProjectCard, normalizeProject, type ProjectData } from './project';
import { SpoolsCard, normalizeSpools, type SpoolsData } from './spools';
import { VerdictCard, normalizeVerdict, type VerdictData } from './verdict';
import type { CompareOutput } from '../../src/api/goalTypes';

/**
 * Cartes MCP App « même rendu que le site, en plus compact » (docs/research/mcp-cards.md).
 * classifyCard reconnaît la FORME du structuredContent (aucun nom d'outil n'arrive à la vue) ; main.tsx l'appelle avant les autres
 * vues. Les formes sont disjointes de celles des vues existantes (suivi, progression, confirmation, fiche modèle, trajectoires).
 */
export type CardData =
  | { card: 'library'; data: LibraryData }
  | { card: 'compare'; data: CompareOutput }
  | { card: 'costs'; data: CostsData }
  | { card: 'spools'; data: SpoolsData }
  | { card: 'prints'; data: PrintsData }
  | { card: 'printers'; data: PrintersData }
  | { card: 'issues'; data: IssuesData }
  | { card: 'project'; data: ProjectData }
  | { card: 'verdict'; data: VerdictData }
  | { card: 'part'; data: PartData }
  | { card: 'print-sheet'; data: PrintSheetData };

export function classifyCard(s: unknown): CardData | undefined {
  let d: unknown;
  if ((d = normalizePart(s))) return { card: 'part', data: d as PartData }; // fiche pièce : counts + profiles + prints (jamais la fiche modèle, qui a files)
  if ((d = normalizePrintSheet(s))) return { card: 'print-sheet', data: d as PrintSheetData };
  if ((d = normalizeProject(s))) return { card: 'project', data: d as ProjectData }; // avant les coûts : list_projects a aussi une clé `projects`
  if ((d = normalizeVerdict(s))) return { card: 'verdict', data: d as VerdictData };
  if ((d = normalizeCompare(s))) return { card: 'compare', data: d as CompareOutput };
  if ((d = normalizeLibrary(s))) return { card: 'library', data: d as LibraryData };
  if ((d = normalizeCosts(s))) return { card: 'costs', data: d as CostsData };
  if ((d = normalizeSpools(s))) return { card: 'spools', data: d as SpoolsData };
  if ((d = normalizePrints(s))) return { card: 'prints', data: d as PrintsData };
  if ((d = normalizePrinters(s))) return { card: 'printers', data: d as PrintersData };
  if ((d = normalizeIssues(s))) return { card: 'issues', data: d as IssuesData };
  return undefined;
}

export function CardView({ app, card, onOpen }: { app: CardApp; card: CardData; onOpen: CardProps<unknown>['onOpen'] }) {
  switch (card.card) {
    case 'library': return <LibraryCard app={app} data={card.data} onOpen={onOpen} />;
    case 'compare': return <CompareCard app={app} data={card.data} onOpen={onOpen} />;
    case 'costs': return <CostsCard app={app} data={card.data} onOpen={onOpen} />;
    case 'spools': return <SpoolsCard app={app} data={card.data} onOpen={onOpen} />;
    case 'prints': return <PrintsCard app={app} data={card.data} onOpen={onOpen} />;
    case 'printers': return <PrintersCard app={app} data={card.data} onOpen={onOpen} />;
    case 'issues': return <IssuesCard app={app} data={card.data} onOpen={onOpen} />;
    case 'project': return <ProjectCard app={app} data={card.data} onOpen={onOpen} />;
    case 'verdict': return <VerdictCard app={app} data={card.data} onOpen={onOpen} />;
    case 'part': return <PartCard app={app} data={card.data} onOpen={onOpen} />;
    case 'print-sheet': return <PrintSheetCard app={app} data={card.data} onOpen={onOpen} />;
  }
}

export type { CardApp };
