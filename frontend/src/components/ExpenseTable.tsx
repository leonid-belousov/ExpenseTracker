import { useI18n } from '../i18n/I18nProvider';
import type { Expense } from '../types';

interface Props {
  items: Expense[];
  currency: string;
  stale: boolean;
  onEdit: (e: Expense) => void;
  onDelete: (e: Expense) => void;
}

export function ExpenseTable({ items, currency, stale, onEdit, onDelete }: Props) {
  const { m, money, date } = useI18n();
  const t = m.table;
  return (
    <table className={stale ? 'expenses stale' : 'expenses'}>
      <thead>
        <tr>
          <th>{t.date}</th>
          <th>{t.member}</th>
          <th>{t.category}</th>
          <th className="num">{t.amount}</th>
          <th>{t.comment}</th>
          <th aria-label={t.actions} />
        </tr>
      </thead>
      <tbody>
        {items.map((e) => (
          <tr key={e.id}>
            <td data-label={t.date}>{date(e.spent_at)}</td>
            <td data-label={t.member}>{e.member_name || e.user_id}</td>
            <td data-label={t.category}>{m.categories[e.category] ?? e.category}</td>
            <td data-label={t.amount} className="num">
              {money(e.amount, currency)}
            </td>
            <td data-label={t.comment} className="comment">
              {e.comment ?? '—'}
            </td>
            <td className="row-actions">
              <button type="button" className="btn small" onClick={() => onEdit(e)}>
                {t.edit}
              </button>
              <button type="button" className="btn small danger" onClick={() => onDelete(e)}>
                {t.delete}
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
