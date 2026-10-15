import { describe, expect, it } from 'vitest';
import { loadLocale } from './I18nProvider';
import { DEFAULT_LOCALE, dictionaries, LOCALES } from './messages';

// Flattens a dictionary into "a.b.c" -> value for structural comparison.
function flatten(obj: object, prefix = ''): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === 'object' && !Array.isArray(v) && key !== 'meta.dateOptions') Object.assign(out, flatten(v, key));
    else out[key] = v;
  }
  return out;
}

describe('dictionaries', () => {
  it('Russian is the default language', () => {
    expect(DEFAULT_LOCALE).toBe('ru');
    expect(LOCALES).toEqual(['ru', 'en']);
  });

  it('every language has exactly the same keys and message arities as Russian', () => {
    const reference = flatten(dictionaries.ru);
    for (const locale of LOCALES) {
      const d = flatten(dictionaries[locale]);
      expect(Object.keys(d).sort(), locale).toEqual(Object.keys(reference).sort());
      for (const [key, value] of Object.entries(reference)) {
        expect(typeof d[key], `${locale}:${key}`).toBe(typeof value);
        if (typeof value === 'function') expect((d[key] as () => string).length, `${locale}:${key}`).toBe(value.length);
        if (typeof value === 'string' && key !== 'meta.htmlLang') expect(d[key], `${locale}:${key}`).not.toBe('');
      }
    }
  });

  it('English strings are actually translated (no Cyrillic left)', () => {
    const en = flatten(dictionaries.en);
    const sample: Record<string, unknown[]> = {
      'list.summary': [3, '10.00 RUB'],
      'list.loadFailed': ['x'],
      'list.configFailed': ['x'],
      'list.page': [1, 2],
      'dialog.amount': ['RUB'],
      'dialog.notSaved': ['x'],
      'remove.confirm': ['1.00 RUB', 'Food', 'Oct 1, 2026', 'Bob'],
      'remove.failed': ['x'],
      'apiErrors.proxy': [502],
      'apiErrors.unknown': [418],
    };
    for (const [key, value] of Object.entries(en)) {
      const text = typeof value === 'function' ? (value as (...a: unknown[]) => string)(...(sample[key] ?? [])) : String(value);
      expect(text, key).not.toMatch(/[А-Яа-яЁё]/);
    }
  });

  it('interpolates parameters', () => {
    expect(dictionaries.ru.list.page(2, 5)).toBe('Страница 2 из 5');
    expect(dictionaries.en.list.page(2, 5)).toBe('Page 2 of 5');
    expect(dictionaries.en.list.summary(3, '1,200.00 RUB')).toBe('Found: 3 · Total: 1,200.00 RUB');
  });
});

describe('locale persistence', () => {
  it('falls back to Russian when nothing (or garbage) is stored', () => {
    expect(loadLocale({ getItem: () => null })).toBe('ru');
    expect(loadLocale({ getItem: () => 'de' })).toBe('ru');
    expect(loadLocale(undefined)).toBe('ru');
  });

  it('restores a saved language', () => {
    expect(loadLocale({ getItem: () => 'en' })).toBe('en');
  });

  it('survives storage that throws (private mode, blocked storage)', () => {
    expect(
      loadLocale({
        getItem: () => {
          throw new Error('SecurityError');
        },
      }),
    ).toBe('ru');
  });
});
