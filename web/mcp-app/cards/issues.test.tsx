// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { IssuesCard, normalizeIssues } from './issues';
import { classifyCard } from './index';
import { mockApp, mount } from './testkit';

let m: ReturnType<typeof mount>;
afterEach(() => m?.unmount());

const ISSUES = {
  issues: [
    { id: 'iss_aaaa', title: 'get_print_status garde 42 %', severity: 'P1', tool: 'get_print_status', observed: 'toujours 42 %', occurrences: 3, state: 'new', last_seen_at: '2026-11-10T09:00:00Z', created_at: 'x', updated_at: 'x' },
    { id: 'iss_bbbb', title: 'Fausse alerte de bobine', severity: 'P2', tool: 'list_spools', occurrences: 1, regressions: 1, state: 'fixed', fix_commit: '3f9a1c2' },
  ],
  total: 2, counts: { new: 1, seen: 0, fixed: 1, dismissed: 0 },
};

describe('carte Dysfonctionnements', () => {
  it('reconnaît list_issues seulement', () => {
    expect(normalizeIssues(ISSUES)?.issues).toHaveLength(2);
    expect(classifyCard(ISSUES)?.card).toBe('issues');
    expect(normalizeIssues({ issues: [{ id: 'x', title: 't' }], counts: {} })).toBeUndefined();
    expect(normalizeIssues({ issues: [], total: 0 })).toBeUndefined();
    expect(classifyCard({ printers: [] })?.card).toBe('printers');
  });

  it('affiche gravité, outil, occurrences, état et commit ; lecture seule', async () => {
    m = mount();
    await m.render(<IssuesCard app={mockApp()} data={normalizeIssues(ISSUES)!} onOpen={() => {}} />);
    const t = m.text();
    expect(t).toContain('get_print_status garde 42 %'); expect(t).toContain('P1'); expect(t).toContain('3 occurrences'); expect(t).toContain('nouveau');
    expect(t).toContain('Corrigé par le commit 3f9a1c2'); expect(t).toContain('revenu 1 fois'); expect(t).toContain('Observé : toujours 42 %');
    expect(m.host.querySelectorAll('button')).toHaveLength(0); // aucun geste : ni état, ni impression
  });

  it('liste vide', async () => {
    m = mount();
    await m.render(<IssuesCard app={mockApp()} data={{ issues: [], total: 0, counts: {} }} onOpen={() => {}} />);
    expect(m.text()).toContain('Aucun constat ne correspond');
  });
});
