// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { GLOSSARY, ISSUE_STATE, JOB_STATE, LOG_OUTCOME, PRINT_STATE, QUEUE_STATE, RESULT_OUTCOME, SPOOL_STATUS, finalOutcomeTitle, printerPill, printStateLabel } from '@shared/labels';
import { stateText } from './pages/Print';

type Src = Record<string, string>;
const raw = (m: Record<string, unknown>): Src => Object.fromEntries(Object.entries(m).map(([k, v]) => [k, String(v)]));
const site = raw(import.meta.glob(['./**/*.tsx', '!./**/*.test.tsx'], { query: '?raw', import: 'default', eager: true }));
const shared = raw(import.meta.glob(['../shared/**/*.tsx', '!../shared/**/*.test.tsx'], { query: '?raw', import: 'default', eager: true }));
const cards = raw(import.meta.glob(['../mcp-app/**/*.tsx', '!../mcp-app/**/*.test.tsx'], { query: '?raw', import: 'default', eager: true }));
const apis = raw(import.meta.glob(['./api/*.ts', '!./api/*.test.ts'], { query: '?raw', import: 'default', eager: true }));
/** Les cartes affichées dans Claude (mcp-app) gardent quelques termes techniques : leur alignement est le chantier UX11 (vague U3). */
const all: Src = { ...site, ...shared, ...apis };

/** Lignes de code qui peuvent s'afficher : ni commentaires, ni imports, ni détails techniques repliés. */
function visibleLines(src: string): string[] {
  return src.split('\n').filter((l) => {
    const t = l.trim();
    if (t.startsWith('//') || t.startsWith('*') || t.startsWith('/*') || t.startsWith('import ')) return false;
    if (t.includes('details=')) return false; // « Détails techniques » : exception de la spec (CA-V1)
    return true;
  }).map((l) => l.replace(/\/\*.*?\*\//g, '').replace(/\s\/\/ .*$/, ''));
}

describe('vocabulaire sans jargon (CA-V1)', () => {
  const FORBIDDEN: Array<[RegExp, string]> = [
    [/\bbambud\b/i, 'bambud'], [/\bFirmware\b/, 'Firmware'], [/\bBackend\b/, 'Backend'], [/\bHMS\b/, 'HMS'],
    [/eyebrow">Espace/, 'surtitre « Espace »'], [/>Espace</, 'surtitre « Espace »'],
  ];
  for (const [file, src] of Object.entries(all)) {
    if (file.endsWith('pages/Design.tsx')) continue; // page de spécimen (exception listée)
    it(`${file}`, () => {
      const lines = visibleLines(src);
      for (const [re, name] of FORBIDDEN) {
        const hit = lines.find((l) => re.test(l));
        expect(hit, `terme interdit « ${name} » : ${hit?.trim().slice(0, 140)}`).toBeUndefined();
      }
    });
  }

  it('aucun libellé de la table unique ne montre un code technique', () => {
    for (const t of [PRINT_STATE, QUEUE_STATE, SPOOL_STATUS, LOG_OUTCOME, RESULT_OUTCOME, JOB_STATE, ISSUE_STATE]) {
      for (const [k, v] of Object.entries(t)) {
        expect(v.label, k).not.toMatch(/UPLOADED|START_UNKNOWN|HMS|bambud|slicer/);
        expect(v.label.length, k).toBeGreaterThan(0);
      }
    }
    expect(PRINT_STATE.UPLOADED.label).toBe('Envoyée à l\'imprimante');
    expect(PRINT_STATE.START_UNKNOWN.label).toBe('Lancement à vérifier');
    expect(GLOSSARY.length).toBeGreaterThan(8);
  });
});

describe('une seule table de libellés (CA-V2)', () => {
  it('plus de table locale STATE_LABEL, QSTATE, STATUS, OUTCOME ni de statePill', () => {
    for (const [file, src] of Object.entries(all)) {
      const lines = visibleLines(src).join('\n');
      expect(lines, file).not.toMatch(/\b(STATE_LABEL|QSTATE|statePill)\b/);
      expect(lines, file).not.toMatch(/\bconst (STATUS|OUTCOME)\b/);
    }
  });
  it('le site et les cartes lisent la même table', () => {
    expect(site['./pages/Print.tsx']).toContain("from '@shared/labels'");
    expect(cards['../mcp-app/live.tsx']).toContain("from '@shared/labels'");
    expect(cards['../mcp-app/progress.tsx']).toContain("from '@shared/labels'");
    // même donnée → même libellé : page (stateText) et table partagée (utilisée par la carte de suivi)
    for (const s of Object.keys(PRINT_STATE)) {
      if (['PREPARED', 'SLICING', 'UPLOADING', 'PRINTING'].includes(s)) continue; // ces états ajoutent un pourcentage quand il est connu
      expect(stateText({ state: s } as never)).toBe(printStateLabel(s).label);
    }
    expect(finalOutcomeTitle('failed')).toBe('Impression échouée');
  });
  it('la pastille d\'une imprimante a un seul vocabulaire', () => {
    expect(printerPill(undefined, true)).toEqual({ cls: 'fail', label: 'Statut indisponible' });
    expect(printerPill({ reachable: false, state: 'idle' }).label).toBe('Injoignable');
    expect(printerPill({ reachable: true, state: 'printing' })).toEqual({ cls: 'run', label: 'Imprime' });
    expect(printerPill({ reachable: true, state: 'idle' }).label).toBe('Prête');
  });
});

describe('structure des écrans (CA-N7, UX1)', () => {
  it('plus de surtitre « Espace », de h1.page-title ni de fil d\'Ariane écrit à la main dans les pages', () => {
    for (const [file, src] of Object.entries(site)) {
      if (!file.startsWith('./pages/') || file.endsWith('Design.tsx')) continue;
      expect(src, file).not.toMatch(/eyebrow">Espace/);
      expect(src, file).not.toContain('page-title');
      expect(src, file).not.toContain('mdl-crumb');
    }
  });
});

describe('le lancement reste une décision humaine (spec § 7)', () => {
  it('api.startPrint n\'est appelé que dans l\'écran des impressions (composant de confirmation)', () => {
    const callers = Object.entries(site).filter(([, src]) => /\.startPrint\(/.test(src)).map(([f]) => f);
    expect(callers).toEqual(['./pages/Print.tsx']);
    expect(site['./pages/Home.tsx']).not.toMatch(/startPrint|cancelPrint|stopPrint|resumePrint/);
  });
  it('aucun bouton de l\'interface n\'arrête ni ne reprend une impression ; l\'arrêt passe par la page serveur', () => {
    for (const [file, src] of Object.entries(site)) {
      expect(src, file).not.toMatch(/api\.(stopPrint|resumePrint|pausePrint)/);
    }
  });
});
