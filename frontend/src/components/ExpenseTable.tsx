import { humanDate } from '../dates';
import { CATEGORY_LABELS, formatMoney } from '../format';
import type { Expense } from '../types';

interface Props {
  items: Expense[];
  currency: string;
  stale: boolean;
  onEdit: (e: Expense) => void;
  onDelete: (e: Expense) => void;
}

export function ExpenseTable({ items, currency, stale, onEdit, onDelete }: Props) {
  return (
    <table className={stale ? 'expenses stale' : 'expenses'}>
      <thead>
        <tr>
          <th>Дата</th>
          <th>Член семьи</th>
          <th>Категория</th>
          <th className="num">Сумма</th>
          <th>Комментарий</th>
          <th aria-label="Действия" />
        </tr>
      </thead>
      <tbody>
        {items.map((e) => (
          <tr key={e.id}>
            <td data-label="Дата">{humanDate(e.spent_at)}</td>
            <td data-label="Член семьи">{e.member_name || e.user_id}</td>
            <td data-label="Категория">{CATEGORY_LABELS[e.category] ?? e.category}</td>
            <td data-label="Сумма" className="num">
              {formatMoney(e.amount, currency)}
            </td>
            <td data-label="Комментарий" className="comment">
              {e.comment ?? '—'}
            </td>
            <td className="row-actions">
              <button type="button" className="btn small" onClick={() => onEdit(e)}>
                Изменить
              </button>
              <button type="button" className="btn small danger" onClick={() => onDelete(e)}>
                Удалить
              </button>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
