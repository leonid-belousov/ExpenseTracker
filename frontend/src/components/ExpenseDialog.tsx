import { useEffect, useRef, useState, type FormEvent } from 'react';
import { ApiError, describeError } from '../api';
import { validateDraft, type ExpenseDraft, type ValidationErrors } from '../format';
import { useI18n } from '../i18n/I18nProvider';
import type { AppConfig, Expense, ExpenseField, ExpenseInput, FieldErrors } from '../types';

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
  const { m } = useI18n();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [draft, setDraft] = useState(() => initialDraft(expense, defaultMemberId, config.today));
  // Errors are kept language-neutral (codes, field names, the error object)
  // and translated at render time, so switching language re-renders them.
  const [clientErrors, setClientErrors] = useState<ValidationErrors>({});
  const [serverFields, setServerFields] = useState<FieldErrors>({});
  const [saveError, setSaveError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const d = dialogRef.current;
    if (d && !d.open) d.showModal();
  }, []);

  const set = (patch: Partial<ExpenseDraft>) => setDraft((d) => ({ ...d, ...patch }));

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setSaveError(null);
    setServerFields({});
    const { input, errors } = validateDraft(draft, config.today);
    setClientErrors(errors);
    if (!input) return;
    setSaving(true);
    try {
      await onSave(input);
    } catch (e) {
      setServerFields(e instanceof ApiError ? e.fields : {});
      setSaveError(e);
    } finally {
      setSaving(false);
    }
  }

  function fieldError(field: ExpenseField) {
    const code = clientErrors[field];
    const text = code ? m.validation[code] : serverFields[field] ? m.serverField[field] : null;
    return text ? <span className="field-error">{text}</span> : null;
  }

  const saveErrorText =
    saveError === null
      ? null
      : saveError instanceof ApiError && saveError.kind === 'notFound'
        ? m.dialog.alreadyDeleted
        : m.dialog.notSaved(describeError(saveError, m));

  return (
    <dialog ref={dialogRef} onCancel={onClose} onClose={onClose}>
      <form className="edit-form" noValidate onSubmit={submit}>
        <h2>{expense ? m.dialog.editTitle : m.dialog.createTitle}</h2>
        <label>
          {m.dialog.member}
          <select value={draft.userId} onChange={(e) => set({ userId: e.target.value })}>
            <option value="">{m.dialog.choose}</option>
            {config.members.map((mem) => (
              <option key={mem.id} value={String(mem.id)}>
                {mem.name}
              </option>
            ))}
          </select>
          {fieldError('user_id')}
        </label>
        <label>
          {m.dialog.amount(config.currency)}
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
          {m.dialog.category}
          <select value={draft.category} onChange={(e) => set({ category: e.target.value })}>
            <option value="">{m.dialog.choose}</option>
            {config.categories.map((c) => (
              <option key={c} value={c}>
                {m.categories[c]}
              </option>
            ))}
          </select>
          {fieldError('category')}
        </label>
        <label>
          {m.dialog.date}
          <input type="date" max={config.today} value={draft.spentAt} onChange={(e) => set({ spentAt: e.target.value })} />
          {fieldError('spent_at')}
        </label>
        <label>
          {m.dialog.comment}
          <input maxLength={500} value={draft.comment} onChange={(e) => set({ comment: e.target.value })} />
          {fieldError('comment')}
        </label>
        {saveErrorText && (
          <p className="banner" role="alert">
            {saveErrorText}
          </p>
        )}
        <div className="actions">
          <button type="button" className="btn" onClick={onClose} disabled={saving}>
            {m.dialog.cancel}
          </button>
          <button type="submit" className="btn primary" disabled={saving}>
            {saving ? m.dialog.saving : m.dialog.save}
          </button>
        </div>
      </form>
    </dialog>
  );
}
