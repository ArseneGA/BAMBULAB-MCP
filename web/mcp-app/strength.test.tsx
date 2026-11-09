// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { StrengthCard, extractStrength, type StrengthResult } from './strength';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLElement; let root: Root;
beforeEach(() => { host = document.createElement('div'); document.body.appendChild(host); root = createRoot(host); });
afterEach(() => { act(() => root.unmount()); host.remove(); });

const R = (extra: Partial<StrengthResult> = {}): StrengthResult => ({
  verdict: 'ok', verdict_text: 'tient', safety_factor_achieved: 3.24, safety_factor_required: 2, max_von_mises_mpa: 7.5, allowable_xy_mpa: 24.3, max_displacement_mm: 1.234,
  critical_note: 'dans la zone 2', inputs: [{ name: 'charge 1', value: '40 N', source: 'assumed' }, { name: 'matière', value: 'PETG', source: 'user' }],
  recommendations: ['Épaissir le bras.'], measured: ['géométrie'], calculated: ['éléments finis'], estimated: ['matière'], ...extra,
});

describe('extractStrength', () => {
  it('lit assess_strength et get_strength_report', () => {
    expect(extractStrength({ state: 'done', result: R() })?.verdict).toBe('ok');
    expect(extractStrength({ detail: { result: R({ verdict: 'risque' }) } })?.verdict).toBe('risque');
    expect(extractStrength({ state: 'running', job_id: 'job_1' })).toBeUndefined();
    expect(extractStrength(undefined)).toBeUndefined();
  });
});

describe('StrengthCard', () => {
  it('affiche verdict, marge, image et provenance des entrées', async () => {
    await act(async () => { root.render(<StrengthCard view={{ result: R({ based_on_assumptions: true }), testId: 'st_1', images: [{ data: 'AAAA', mime: 'image/png' }] }} />); });
    const t = host.textContent ?? '';
    expect(t).toContain('Tient avec une marge de ×3,2');
    expect(t).toContain('basé sur des hypothèses');
    expect(t).toContain('hypothèse');
    expect(t).toContain('7,5 MPa');
    expect(host.querySelector('img')?.getAttribute('src')).toBe('data:image/png;base64,AAAA');
    const btn = host.querySelector('button.str-img') as HTMLButtonElement;
    await act(async () => { btn.click(); });
    expect(btn.getAttribute('aria-pressed')).toBe('true');
  });
  it('signale le risque de casse', async () => {
    await act(async () => { root.render(<StrengthCard view={{ result: R({ verdict: 'risque', safety_factor_achieved: 0.62 }), images: [] }} />); });
    expect(host.textContent).toContain('Risque de casse');
    expect(host.querySelector('.str-err')).not.toBeNull();
    expect(host.textContent).toContain('ne remplace pas un essai réel');
  });
});
