import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError } from '../api';
import { CATEGORY_LABELS, validateDraft, type ExpenseDraft } from '../format';
import type { AppConfig, Expense, ExpenseInput, FieldErrors } from '../types';

interface Props {
  config: AppConfig;
  /** null = create a new expense */
  expense: Expense | null;
  defaultMemberId: string;
  onSave: (input: ExpenseInput) => Promise<void>;
  onClose: () => void;
}

function initialDraft(expense: Expense | null, defaultMemberId: string, today: string): ExpenseDraft {
  return expense
    ? {
        userId: String(expense.user_id),
        amount: expense.amount,
        category: expense.category,
        spentAt: expense.spent_at,
        comment: expense.comment ?? '',
      }
    : { userId: defaultMemberId, amount: '', category: '', spentAt: today, comment: '' };
}

export function ExpenseDialog({ config, expense, defaultMemberId, onSave, onClose }: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [draft, setDraft] = useState(() => initialDraft(expense, defaultMemberId, config.today));
  const [errors, setErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const d = dialogRef.current;
    if (d && !d.open) d.showModal();
  }, []);

  const set = (patch: Partial<ExpenseDraft>) => setDraft((d) => ({ ...d, ...patch }));

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setFormError(null);
    const { input, errors: clientErrors } = validateDraft(draft, config.today);
    setErrors(clientErrors);
    if (!input) return;
    setSaving(true);
    try {
      await onSave(input);
    } catch (e) {
      if (e instanceof ApiError) {
        setErrors(e.fields);
        setFormError(e.status === 404 ? 'Трата уже удалена. Обновите список.' : 'Не сохранено: ' + e.message);
      } else {
        setFormError('Не сохранено: непредвиденная ошибка.');
      }
    } finally {
      setSaving(false);
    }
  }

  const fieldError = (k: keyof FieldErrors) => (errors[k] ? <span className="field-error">{errors[k]}</span> : null);

  return (
    <dialog ref={dialogRef} onCancel={onClose} onClose={onClose}>
      <form className="edit-form" noValidate onSubmit={submit}>
        <h2>{expense ? 'Изменить трату' : 'Новая трата'}</h2>
        <label>
          Член семьи
          <select value={draft.userId} onChange={(e) => set({ userId: e.target.value })}>
            <option value="">— выберите —</option>
            {config.members.map((m) => (
              <option key={m.id} value={String(m.id)}>
                {m.name}
              </option>
            ))}
          </select>
          {fieldError('user_id')}
        </label>
        <label>
          Сумма, {config.currency}
          <input
            inputMode="decimal"
            placeholder="350.00"
            autoFocus
            value={draft.amount}
            onChange={(e) => set({ amount: e.target.value })}
          />
          {fieldError('amount')}
        </label>
        <label>
          Категория
          <select value={draft.category} onChange={(e) => set({ category: e.target.value })}>
            <option value="">— выберите —</option>
            {config.categories.map((c) => (
              <option key={c} value={c}>
                {CATEGORY_LABELS[c]}
              </option>
            ))}
          </select>
          {fieldError('category')}
        </label>
        <label>
          Дата траты
          <input type="date" max={config.today} value={draft.spentAt} onChange={(e) => set({ spentAt: e.target.value })} />
          {fieldError('spent_at')}
        </label>
        <label>
          Комментарий
          <input maxLength={500} value={draft.comment} onChange={(e) => set({ comment: e.target.value })} />
          {fieldError('comment')}
        </label>
        {formError && (
          <p className="banner" role="alert">
            {formError}
          </p>
        )}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose} disabled={saving}>
            Отмена
          </button>
          <button type="submit" className="btn primary" disabled={saving}>
            {saving ? 'Сохранение…' : 'Сохранить'}
          </button>
        </div>
      </form>
    </dialog>
  );
}
