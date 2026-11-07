import { describe, expect, it } from 'vitest';
import { initialForm, num, paramsFromForm } from './libraryApi';
import type { ParamSpec, TemplateInfo } from './libraryTypes';

const specs: ParamSpec[] = [
  { name: 'diameter', type: 'number', unit: 'mm', default: 6, min: 1, max: 50, description: 'd', auto: 'hole:vertical' },
  { name: 'count', type: 'integer', default: 2, min: 1, max: 10, description: 'n' },
  { name: 'mode', type: 'enum', default: 'a', options: ['a', 'b'], description: 'm' },
  { name: 'flag', type: 'boolean', default: false, description: 'f' },
];

describe('paramsFromForm', () => {
  it('ne renvoie que les champs renseignés et les type', () => {
    const { params, errors } = paramsFromForm(specs, { diameter: '8,5', count: '3', mode: 'b', flag: 'true' });
    expect(errors).toEqual({});
    expect(params).toEqual({ diameter: 8.5, count: 3, mode: 'b', flag: true });
  });

  it('laisse vides les champs non saisis (défaut ou valeur calculée)', () => {
    expect(paramsFromForm(specs, { diameter: '  ', mode: '' }).params).toEqual({});
  });

  it('refuse les valeurs hors schéma avec un message par champ', () => {
    const { params, errors } = paramsFromForm(specs, { diameter: '0.5', count: '2.5', mode: 'z' });
    expect(params).toEqual({});
    expect(errors.diameter).toContain('minimum 1');
    expect(errors.count).toBe('entier attendu');
    expect(errors.mode).toContain('a, b');
    expect(paramsFromForm(specs, { diameter: '99' }).errors.diameter).toContain('maximum 50');
    expect(paramsFromForm(specs, { diameter: 'abc' }).errors.diameter).toBe('nombre attendu');
  });
});

describe('initialForm', () => {
  it('reprend les paramètres figés d\'une variante', () => {
    const t = { fixed_params: { diameter: 7, mode: 'b' } } as unknown as TemplateInfo;
    expect(initialForm(t)).toEqual({ diameter: '7', mode: 'b' });
    expect(initialForm({} as TemplateInfo)).toEqual({});
  });
});

describe('num', () => {
  it('accepte la virgule décimale et ignore le vide', () => {
    expect(num('0,35')).toBe(0.35);
    expect(num(' ')).toBeUndefined();
    expect(num('x')).toBeUndefined();
  });
});
