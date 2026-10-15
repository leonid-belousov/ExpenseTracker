import type { Category, ExpenseInput, FieldErrors } from './types';

// Localised labels only; the API and storage use the canonical keys.
export const CATEGORY_LABELS: Record<Category, string> = {
  food: 'Еда',
  transport: 'Транспорт',
  housing: 'Жильё',
  entertainment: 'Развлечения',
  health: 'Здоровье',
  shopping: 'Покупки',
  other: 'Другое',
};

/** Formats an exact decimal string ("1200.5" / "1200.50") without floats. */
export function formatMoney(amount: string, currency: string): string {
  const [intPart, frac = ''] = amount.split('.');
  const grouped = intPart.replace(/\B(?=(\d{3})+(?!\d))/g, ' ');
  return `${grouped},${(frac + '00').slice(0, 2)} ${currency}`;
}

/** Normalises user input like "1 200,5" to the API form "1200.5". */
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

/** Client-side checks mirroring the backend rules. The backend remains the
 * authority; its field errors are shown as well. */
export function validateDraft(d: ExpenseDraft, today: string): { input?: ExpenseInput; errors: FieldErrors } {
  const errors: FieldErrors = {};
  const amount = normalizeAmount(d.amount);
  if (!d.userId) errors.user_id = 'Выберите члена семьи.';
  if (!/^\d+(\.\d{1,2})?$/.test(amount)) {
    errors.amount = 'Введите положительную сумму, не больше двух знаков после запятой.';
  } else if (/^0+(\.0+)?$/.test(amount)) {
    errors.amount = 'Сумма должна быть больше нуля.';
  }
  if (!d.category) errors.category = 'Выберите категорию.';
  if (!d.spentAt) errors.spent_at = 'Укажите дату.';
  else if (d.spentAt > today) errors.spent_at = 'Дата не может быть в будущем.';
  if (d.comment.trim().length > 500) errors.comment = 'Комментарий не длиннее 500 символов.';
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
