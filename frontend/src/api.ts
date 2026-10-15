import { addDays } from './dates';
import type { AppConfig, Expense, ExpenseInput, ExpensePage, FieldErrors, Filters } from './types';

// Same-origin base path: nginx (production) or the Vite dev server proxies
// /api to the Go backend.
const API = '/api';

export class ApiError extends Error {
  readonly status: number;
  readonly fields: FieldErrors;

  constructor(status: number, message: string, fields: FieldErrors = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.fields = fields;
  }
}

interface ErrorBody {
  error?: { code?: string; message?: string; fields?: Record<string, string> };
}

async function request<T>(path: string, init: RequestInit = {}, fetchImpl: typeof fetch = fetch): Promise<T> {
  let res: Response;
  try {
    res = await fetchImpl(API + path, {
      ...init,
      headers: init.body ? { 'Content-Type': 'application/json' } : undefined,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err;
    throw new ApiError(0, 'Сервер недоступен. Проверьте подключение и повторите попытку.');
  }
  if (res.status === 204) return undefined as T;
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    /* non-JSON error page from a proxy */
  }
  if (!res.ok) {
    const e = (body as ErrorBody | null)?.error;
    const fallback =
      res.status >= 502 && res.status <= 504
        ? `Сервер API недоступен (${res.status}). Попробуйте позже.`
        : `Ошибка сервера (${res.status})`;
    throw new ApiError(res.status, e?.message ?? fallback, (e?.fields ?? {}) as FieldErrors);
  }
  return body as T;
}

/** Builds list query parameters. The API range is half-open, so the
 * inclusive "to" date from the UI is sent as the following day. */
export function buildListQuery(f: Filters, limit: number, offset: number): URLSearchParams {
  const p = new URLSearchParams();
  if (f.userId) p.set('user_id', f.userId);
  if (f.category) p.set('category', f.category);
  if (f.from) p.set('from', f.from);
  if (f.to) p.set('to', addDays(f.to, 1));
  p.set('limit', String(limit));
  p.set('offset', String(offset));
  return p;
}

export const api = {
  config: () => request<AppConfig>('/config'),
  list: (f: Filters, limit: number, offset: number, signal?: AbortSignal) =>
    request<ExpensePage>('/expenses?' + buildListQuery(f, limit, offset).toString(), { signal }),
  create: (input: ExpenseInput) => request<Expense>('/expenses', { method: 'POST', body: JSON.stringify(input) }),
  update: (id: number, input: ExpenseInput) =>
    request<Expense>(`/expenses/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  remove: (id: number) => request<void>(`/expenses/${id}`, { method: 'DELETE' }),
};

export { request as requestForTest };
