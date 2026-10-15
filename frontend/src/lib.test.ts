import { describe, expect, it, vi } from 'vitest';
import { ApiError, buildListQuery, describeError, request } from './api';
import { addDays, presetRange } from './dates';
import { formatDate, formatMoney, normalizeAmount, validateDraft } from './format';
import { dictionaries } from './i18n/messages';

describe('dates', () => {
  it('weeks start on Monday, including when today is Sunday', () => {
    expect(presetRange('this_week', '2026-10-15')).toEqual(['2026-10-12', '2026-10-18']); // Thursday
    expect(presetRange('this_week', '2026-10-18')).toEqual(['2026-10-12', '2026-10-18']); // Sunday
    expect(presetRange('this_week', '2026-10-19')).toEqual(['2026-10-19', '2026-10-25']); // Monday
  });

  it('computes inclusive month ranges across year boundaries', () => {
    expect(presetRange('this_month', '2026-02-10')).toEqual(['2026-02-01', '2026-02-28']);
    expect(presetRange('last_month', '2026-01-10')).toEqual(['2025-12-01', '2025-12-31']);
    expect(presetRange('all', '2026-01-10')).toEqual(['', '']);
  });

  it('adds days without time-zone drift', () => {
    expect(addDays('2026-03-28', 2)).toBe('2026-03-30');
    expect(addDays('2026-12-31', 1)).toBe('2027-01-01');
  });
});

describe('list query', () => {
  it('sends the inclusive UI end date as an exclusive API bound', () => {
    const q = buildListQuery({ userId: '111', category: 'food', from: '2026-10-12', to: '2026-10-18' }, 50, 0);
    expect(q.get('from')).toBe('2026-10-12');
    expect(q.get('to')).toBe('2026-10-19');
    expect(q.get('user_id')).toBe('111');
    expect(q.get('category')).toBe('food');
  });

  it('omits empty filters', () => {
    const q = buildListQuery({ userId: '', category: '', from: '', to: '' }, 50, 100);
    expect([...q.keys()]).toEqual(['limit', 'offset']);
    expect(q.get('offset')).toBe('100');
  });
});

describe('locale-aware formatting', () => {
  it('formats exact decimal strings per locale without floats', () => {
    expect(formatMoney('1200.5', 'RUB', 'ru')).toBe('1 200,50 RUB');
    expect(formatMoney('1200.5', 'RUB', 'en')).toBe('1,200.50 RUB');
    expect(formatMoney('0.30', 'RUB', 'ru')).toBe('0,30 RUB');
    expect(formatMoney('1234567', 'USD', 'en')).toBe('1,234,567.00 USD');
    expect(formatMoney('99999999999999999.99', 'RUB', 'en')).toBe('99,999,999,999,999,999.99 RUB');
  });

  it('formats calendar dates per locale without shifting the day', () => {
    const { ru, en } = dictionaries;
    expect(formatDate('2026-10-05', ru.meta.intlLocale, ru.meta.dateOptions)).toBe('05.10.2026');
    expect(formatDate('2026-10-05', en.meta.intlLocale, en.meta.dateOptions)).toBe('Oct 5, 2026');
    expect(formatDate('2026-01-01', en.meta.intlLocale, en.meta.dateOptions)).toBe('Jan 1, 2026');
  });

  it('normalises user input', () => {
    expect(normalizeAmount(' 1 200,5 ')).toBe('1200.5');
    expect(normalizeAmount('1 200.50')).toBe('1200.50');
  });
});

describe('draft validation', () => {
  const base = { userId: '111', amount: '350', category: 'food', spentAt: '2026-10-15', comment: '  кофе ' };

  it('accepts a valid draft and trims the comment', () => {
    const { input, errors } = validateDraft(base, '2026-10-15');
    expect(errors).toEqual({});
    expect(input).toEqual({ user_id: 111, amount: '350', category: 'food', spent_at: '2026-10-15', comment: 'кофе' });
  });

  it.each([
    ['0', 'amountPositive'],
    ['0.00', 'amountPositive'],
    ['-5', 'amountInvalid'],
    ['1.234', 'amountInvalid'],
    ['abc', 'amountInvalid'],
  ])('rejects amount %s with code %s', (amount, code) => {
    expect(validateDraft({ ...base, amount }, '2026-10-15').errors.amount).toBe(code);
  });

  it('returns language-neutral codes that every locale can translate', () => {
    const { errors, input } = validateDraft({ ...base, userId: '', category: '', spentAt: '2026-10-16' }, '2026-10-15');
    expect(input).toBeUndefined();
    expect(errors).toEqual({ user_id: 'memberRequired', category: 'categoryRequired', spent_at: 'dateFuture' });
    for (const m of Object.values(dictionaries)) {
      for (const code of Object.values(errors)) expect(m.validation[code!]).toBeTruthy();
    }
  });

  it('sends an empty comment as null', () => {
    expect(validateDraft({ ...base, comment: '   ' }, '2026-10-15').input?.comment).toBeNull();
  });
});

describe('api client', () => {
  const ru = dictionaries.ru;
  const en = dictionaries.en;

  it('classifies API validation errors and keeps raw field errors', async () => {
    const body = { error: { code: 'validation_error', message: 'request validation failed', fields: { amount: 'too precise' } } };
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(body), { status: 400 }));
    const err = await failure(request('/expenses', { method: 'POST', body: '{}' }, fetchImpl));
    expect(err.kind).toBe('validation');
    expect(err.fields).toEqual({ amount: 'too precise' });
    expect(describeError(err, ru)).toBe(ru.apiErrors.validation);
    expect(describeError(err, en)).toBe(en.apiErrors.validation);
    expect(fetchImpl).toHaveBeenCalledWith('/api/expenses', expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }));
  });

  it('describes network, proxy, storage and unknown failures in both languages', async () => {
    const down = await failure(request('/config', {}, vi.fn(async () => Promise.reject(new TypeError('fetch failed')))));
    expect(down.kind).toBe('network');
    expect(describeError(down, en)).toBe(en.apiErrors.network);

    const proxy = await failure(request('/config', {}, vi.fn(async () => new Response('<html>Bad Gateway</html>', { status: 502 }))));
    expect(proxy.kind).toBe('proxy');
    expect(describeError(proxy, ru)).toContain('502');
    expect(describeError(proxy, en)).toContain('502');

    const storage = await failure(
      request('/expenses', {}, vi.fn(async () => new Response(JSON.stringify({ error: { code: 'unavailable', message: 'x' } }), { status: 503 }))),
    );
    expect(storage.kind).toBe('unavailable');

    const teapot = await failure(request('/x', {}, vi.fn(async () => new Response('{}', { status: 418 }))));
    expect(describeError(teapot, en)).toBe(en.apiErrors.unknown(418));
    expect(describeError(new Error('boom'), ru)).toBe(ru.apiErrors.unexpected);
  });

  it('returns undefined for 204', async () => {
    await expect(request('/expenses/1', { method: 'DELETE' }, vi.fn(async () => new Response(null, { status: 204 })))).resolves.toBeUndefined();
  });
});

async function failure(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    if (e instanceof ApiError) return e;
    throw e;
  }
  throw new Error('expected the request to fail');
}
