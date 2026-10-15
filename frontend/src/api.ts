import { addDays } from './dates';
import type { ApiErrorKind, Messages } from './i18n/messages';
import type { AppConfig, Expense, ExpenseInput, ExpensePage, FieldErrors, Filters } from './types';

// Same-origin base path: nginx (production) or the Vite dev server proxies
// /api to the Go backend.
const API = '/api';

/** A failed API call. It carries a language-neutral `kind` (translated by
 * the UI) plus the server's raw field errors, if any. */
export class ApiError extends Error {
  readonly status: number;
  readonly kind: ApiErrorKind;
  readonly fields: FieldErrors;

  constructor(status: number, kind: ApiErrorKind, message: string, fields: FieldErrors = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.kind = kind;
    this.fields = fields;
  }
}

interface ErrorBody {
  error?: { code?: string; message?: string; fields?: Record<string, string> };
}

function kindOf(status: number, code: string | undefined, hasJsonBody: boolean): ApiErrorKind {
  if (status >= 502 && status <= 504 && !hasJsonBody) return 'proxy';
  switch (code) {
    case 'validation_error':
    case 'invalid_body':
    case 'unsupported_media_type':
    case 'body_too_large':
      return 'validation';
    case 'not_found':
      return 'notFound';
    case 'origin_not_allowed':
      return 'forbidden';
    case 'unavailable':
      return 'unavailable';
  }
  if (status === 404) return 'notFound';
  return 'unknown';
}

/** Localised, user-facing description of any error thrown by the client. */
export function describeError(e: unknown, m: Messages): string {
  if (!(e instanceof ApiError)) return m.apiErrors.unexpected;
  switch (e.kind) {
    case 'network':
      return m.apiErrors.network;
    case 'proxy':
      return m.apiErrors.proxy(e.status);
    case 'validation':
      return m.apiErrors.validation;
    case 'notFound':
      return m.apiErrors.notFound;
    case 'forbidden':
      return m.apiErrors.forbidden;
    case 'unavailable':
      return m.apiErrors.unavailable;
    case 'unknown':
      return m.apiErrors.unknown(e.status);
  }
}

export async function request<T>(path: string, init: RequestInit = {}, fetchImpl: typeof fetch = fetch): Promise<T> {
  let res: Response;
  try {
    res = await fetchImpl(API + path, {
      ...init,
      headers: init.body ? { 'Content-Type': 'application/json' } : undefined,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err;
    throw new ApiError(0, 'network', 'network error');
  }
  if (res.status === 204) return undefined as T;
  let body: unknown = null;
  let hasJsonBody = false;
  try {
    body = await res.json();
    hasJsonBody = true;
  } catch {
    /* non-JSON error page from a proxy */
  }
  if (!res.ok) {
    const e = (body as ErrorBody | null)?.error;
    throw new ApiError(
      res.status,
      kindOf(res.status, e?.code, hasJsonBody),
      e?.message ?? `HTTP ${res.status}`,
      (e?.fields ?? {}) as FieldErrors,
    );
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
