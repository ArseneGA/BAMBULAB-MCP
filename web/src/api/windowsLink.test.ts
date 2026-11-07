import { describe, expect, it } from 'vitest';
import { insecureOrigin, windowsLink } from './windowsLink';
import type { Capabilities } from './types';

const caps = (agents: { online: boolean }[], winAvailable = false) =>
  ({ features: [{ id: 'windows_agent', available: winAvailable, note: '' }], agents: agents.map((a, i) => ({ id: `a${i}`, name: `PC${i}`, ...a })) }) as unknown as Capabilities;

describe('windowsLink', () => {
  it('« non utilisé » sans agent jamais appairé (pas d’alerte)', () => {
    expect(windowsLink(true, caps([]))).toBe('unused');
    expect(windowsLink(true, undefined)).toBe('unused');
  });
  it('« hors ligne » seulement si un agent appairé ne répond plus', () => {
    expect(windowsLink(true, caps([{ online: false }]))).toBe('offline');
  });
  it('en ligne si un agent répond ; indisponible si bambud est injoignable', () => {
    expect(windowsLink(true, caps([{ online: false }, { online: true }]))).toBe('online');
    expect(windowsLink(false, caps([]))).toBe('unavailable');
  });
});

describe('insecureOrigin', () => {
  it('http hors loopback seulement', () => {
    expect(insecureOrigin({ protocol: 'http:', hostname: '192.168.0.8' })).toBe(true);
    expect(insecureOrigin({ protocol: 'https:', hostname: '192.168.0.8' })).toBe(false);
    expect(insecureOrigin({ protocol: 'http:', hostname: 'localhost' })).toBe(false);
    expect(insecureOrigin({ protocol: 'http:', hostname: '127.0.0.1' })).toBe(false);
    expect(insecureOrigin({ protocol: 'http:', hostname: '[::1]' })).toBe(false);
  });
});
