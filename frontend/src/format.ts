import type { Locale, ValidationCode } from './i18n/messages';
import type { Category, ExpenseField, ExpenseInput } from './types';

const SEPARATORS: Record<Locale, { group: string; decimal: string }> = {
  ru: { group: ' ', decimal: ',' },
  en: { group: ',', decimal: '.' },
};

/** Formats an exact decimal string ("1200.5" / "1200.50") for display
 * without converting to a float: ru "1 200,50 RUB", en "1,200.50 RUB". */
export function formatMoney(amount: string, currency: string, locale: Locale): string {
  const { group, decimal } = SEPARATORS[locale];
  const negative = amount.startsWith('-');
  const [intPart, frac = ''] = (negative ? amount.slice(1) : amount).split('.');
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, group);
  return `${negative ? '-' : ''}${grouped}${decimal}${(frac + '00').slice(0, 2)} ${currency}`;
}

const dateFormatters = new Map<string, Intl.DateTimeFormat>();

/** Formats a calendar date ("2026-10-15") for display. The date is placed at
 * UTC midnight and formatted in UTC, so the viewer's zone never shifts it. */
export function formatDate(iso: string, intlLocale: string, options: Intl.DateTimeFormatOptions): string {
  const key = intlLocale + JSON.stringify(options);
  let f = dateFormatters.get(key);
  if (!f) {
    f = new Intl.DateTimeFormat(intlLocale, { ...options, timeZone: 'UTC' });
    dateFormatters.set(key, f);
  }
  const [y, m, d] = iso.split('-').map(Number);
  return f.format(new Date(Date.UTC(y, m - 1, d)));
}

/** Normalises user input like "1 200,5" or "1200.5" to the
 * API form "1200.5". A comma is treated as the decimal separator. */
export function normalizeAmount(raw: string): string {
  return raw.trim().replace(/[\s ]/g, '').replace(',', '.');
}

export interface ExpenseDraft {
  userId: string;
  amount: string;
  category: string;
  spentAt: string;
  comment: string;
}

export type ValidationErrors = Partial<Record<ExpenseField, ValidationCode>>;

/** Client-side checks mirroring the backend rules. Returns language-neutral
 * codes; the UI translates them. The backend remains the authority. */
export function validateDraft(d: ExpenseDraft, today: string): { input?: ExpenseInput; errors: ValidationErrors } {
  const errors: ValidationErrors = {};
  const amount = normalizeAmount(d.amount);
  if (!d.userId) errors.user_id = 'memberRequired';
  if (!/^\d+(\.\d{1,2})?$/.test(amount)) errors.amount = 'amountInvalid';
  else if (/^0+(\.0+)?$/.test(amount)) errors.amount = 'amountPositive';
  if (!d.category) errors.category = 'categoryRequired';
  if (!d.spentAt) errors.spent_at = 'dateRequired';
  else if (d.spentAt > today) errors.spent_at = 'dateFuture';
  if (d.comment.trim().length > 500) errors.comment = 'commentTooLong';
  if (Object.keys(errors).length > 0) return { errors };
  return {
    errors,
    input: {
      user_id: Number(d.userId),
      amount,
      category: d.category as Category,
      spent_at: d.spentAt,
      comment: d.comment.trim() || null,
    },
  };
}
