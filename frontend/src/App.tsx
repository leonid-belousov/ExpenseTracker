import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { api, ApiError } from './api';
import { presetRange, humanDate } from './dates';
import { CATEGORY_LABELS, formatMoney } from './format';
import { ExpenseDialog } from './components/ExpenseDialog';
import { ExpenseTable } from './components/ExpenseTable';
import { FiltersBar, rangeError } from './components/FiltersBar';
import type { AppConfig, Expense, ExpenseInput, ExpensePage, Filters } from './types';

const PAGE_SIZE = 50;

type ListState =
  | { status: 'loading'; page: ExpensePage | null }
  | { status: 'ready'; page: ExpensePage }
  | { status: 'error'; message: string };

// `null` = dialog closed; `{ expense: null }` = create; otherwise edit.
type DialogState = { expense: Expense | null } | null;

function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : 'непредвиденная ошибка';
}

export function App() {
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [configError, setConfigError] = useState<string | null>(null);
  const [filters, setFilters] = useState<Filters | null>(null);
  const [offset, setOffset] = useState(0);
  const [list, setList] = useState<ListState>({ status: 'loading', page: null });
  const [reloadKey, setReloadKey] = useState(0);
  const [banner, setBanner] = useState<string | null>(null);
  const [dialog, setDialog] = useState<DialogState>(null);

  useEffect(() => {
    api
      .config()
      .then((c) => {
        setConfig(c);
        const [from, to] = presetRange('this_month', c.today);
        setFilters({ userId: '', category: '', from, to });
      })
      .catch((e) => setConfigError(errorMessage(e)));
  }, []);

  // Load the list whenever filters, page or reloadKey change. Superseded
  // requests are aborted so stale responses never overwrite newer data.
  useEffect(() => {
    if (!filters || rangeError(filters)) return;
    const ctrl = new AbortController();
    setList((prev) => ({ status: 'loading', page: prev.status === 'error' ? null : prev.page }));
    api
      .list(filters, PAGE_SIZE, offset, ctrl.signal)
      .then((page) => setList({ status: 'ready', page }))
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        setList({ status: 'error', message: errorMessage(e) });
      });
    return () => ctrl.abort();
  }, [filters, offset, reloadKey]);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  const changeFilters = (f: Filters) => {
    setOffset(0);
    setFilters(f);
  };

  async function save(input: ExpenseInput) {
    if (dialog?.expense) await api.update(dialog.expense.id, input);
    else await api.create(input);
    // Only reached after the API confirmed the write.
    setDialog(null);
    setBanner(null);
    reload();
  }

  async function remove(e: Expense) {
    if (!config) return;
    const ok = window.confirm(
      `Удалить трату ${formatMoney(e.amount, config.currency)} (${CATEGORY_LABELS[e.category]}, ${humanDate(e.spent_at)}, ${e.member_name})?`,
    );
    if (!ok) return;
    try {
      await api.remove(e.id);
      setBanner(null);
      const page = list.status === 'ready' ? list.page : null;
      if (page && page.items.length === 1 && offset > 0) setOffset(Math.max(0, offset - PAGE_SIZE));
    } catch (err) {
      setBanner(
        err instanceof ApiError && err.status === 404
          ? 'Трата уже была удалена.'
          : 'Не удалось удалить трату: ' + errorMessage(err),
      );
    }
    reload();
  }

  if (configError) {
    return (
      <Shell canAdd={false} onAdd={() => {}}>
        <div className="state error">Не удалось загрузить настройки: {configError}</div>
      </Shell>
    );
  }
  if (!config || !filters) {
    return (
      <Shell canAdd={false} onAdd={() => {}}>
        <div className="state">Загрузка…</div>
      </Shell>
    );
  }

  const page = list.status === 'error' ? null : list.page;
  const pages = page ? Math.max(1, Math.ceil(page.total / PAGE_SIZE)) : 1;

  return (
    <Shell canAdd onAdd={() => setDialog({ expense: null })}>
      {banner && (
        <div className="banner" role="alert">
          {banner}
        </div>
      )}
      <FiltersBar config={config} filters={filters} onChange={changeFilters} />

      <section className="summary" aria-live="polite">
        {page ? `Найдено: ${page.total} · Сумма: ${formatMoney(page.sum, config.currency)}` : '—'}
      </section>

      {list.status === 'error' && <div className="state error">Не удалось загрузить траты: {list.message}</div>}
      {list.status === 'loading' && !page && <div className="state">Загрузка…</div>}
      {page && page.items.length === 0 && list.status === 'ready' && (
        <div className="state empty">Трат по выбранным фильтрам нет.</div>
      )}
      {page && page.items.length > 0 && (
        <ExpenseTable
          items={page.items}
          currency={config.currency}
          stale={list.status === 'loading'}
          onEdit={(e) => setDialog({ expense: e })}
          onDelete={remove}
        />
      )}
      {page && pages > 1 && (
        <nav className="pager">
          <button type="button" className="btn small" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}>
            ← Назад
          </button>
          <span>
            Страница {Math.floor(offset / PAGE_SIZE) + 1} из {pages}
          </span>
          <button
            type="button"
            className="btn small"
            disabled={offset + PAGE_SIZE >= page.total}
            onClick={() => setOffset(offset + PAGE_SIZE)}
          >
            Вперёд →
          </button>
        </nav>
      )}

      {dialog && (
        <ExpenseDialog
          config={config}
          expense={dialog.expense}
          defaultMemberId={filters.userId}
          onSave={save}
          onClose={() => setDialog(null)}
        />
      )}
    </Shell>
  );
}

function Shell({ children, canAdd, onAdd }: { children: ReactNode; canAdd: boolean; onAdd: () => void }) {
  return (
    <>
      <header className="topbar">
        <h1>Семейные расходы</h1>
        <button type="button" className="btn primary" disabled={!canAdd} onClick={onAdd}>
          + Добавить трату
        </button>
      </header>
      <main>{children}</main>
    </>
  );
}
