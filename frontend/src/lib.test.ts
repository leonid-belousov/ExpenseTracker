import { describe, expect, it, vi } from 'vitest';
import { ApiError, buildListQuery, requestForTest } from './api';
import { addDays, humanDate, presetRange } from './dates';
import { formatMoney, normalizeAmount, validateDraft } from './format';

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
    expect(humanDate('2026-10-05')).toBe('05.10.2026');
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

describe('money', () => {
  it('formats exact decimal strings', () => {
    expect(formatMoney('1200.5', 'RUB')).toBe('1 200,50 RUB');
    expect(formatMoney('0.30', 'RUB')).toBe('0,30 RUB');
    expect(formatMoney('1234567', 'RUB')).toBe('1 234 567,00 RUB');
  });

  it('normalises user input', () => {
    expect(normalizeAmount(' 1 200,5 ')).toBe('1200.5');
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
    ['0', 'amount'],
    ['-5', 'amount'],
    ['1.234', 'amount'],
    ['abc', 'amount'],
  ])('rejects amount %s', (amount, field) => {
    expect(validateDraft({ ...base, amount }, '2026-10-15').errors).toHaveProperty(field);
  });

  it('rejects missing member/category and future dates', () => {
    const { errors, input } = validateDraft({ ...base, userId: '', category: '', spentAt: '2026-10-16' }, '2026-10-15');
    expect(input).toBeUndefined();
    expect(Object.keys(errors).sort()).toEqual(['category', 'spent_at', 'user_id']);
  });

  it('sends an empty comment as null', () => {
    expect(validateDraft({ ...base, comment: '   ' }, '2026-10-15').input?.comment).toBeNull();
  });
});

describe('api client', () => {
  it('surfaces API field errors', async () => {
    const body = { error: { code: 'validation_error', message: 'request validation failed', fields: { amount: 'too precise' } } };
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(body), { status: 400 }));
    const err = await failure(requestForTest('/expenses', { method: 'POST', body: '{}' }, fetchImpl));
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(400);
    expect(err.fields).toEqual({ amount: 'too precise' });
    expect(fetchImpl).toHaveBeenCalledWith('/api/expenses', expect.objectContaining({ headers: { 'Content-Type': 'application/json' } }));
  });

  it('reports network failures and non-JSON errors clearly', async () => {
    const down = await failure(requestForTest('/config', {}, vi.fn(async () => Promise.reject(new TypeError('fetch failed')))));
    expect(down).toBeInstanceOf(ApiError);
    expect(down.status).toBe(0);
    const proxy = await failure(requestForTest('/config', {}, vi.fn(async () => new Response('<html>Bad Gateway</html>', { status: 502 }))));
    expect(proxy.message).toContain('502');
  });

  it('returns undefined for 204', async () => {
    await expect(requestForTest('/expenses/1', { method: 'DELETE' }, vi.fn(async () => new Response(null, { status: 204 })))).resolves.toBeUndefined();
  });
});

async function failure(p: Promise<unknown>): Promise<ApiError> {
  try {
    await p;
  } catch (e) {
    return e as ApiError;
  }
  throw new Error('expected the request to fail');
}
