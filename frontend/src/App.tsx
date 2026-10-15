import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { api, ApiError, describeError } from './api';
import { presetRange } from './dates';
import { useI18n } from './i18n/I18nProvider';
import { ExpenseDialog } from './components/ExpenseDialog';
import { ExpenseTable } from './components/ExpenseTable';
import { FiltersBar, hasRangeError } from './components/FiltersBar';
import { LanguageSwitcher } from './components/LanguageSwitcher';
import type { AppConfig, Expense, ExpenseInput, ExpensePage, Filters } from './types';

const PAGE_SIZE = 50;

// Errors are stored as values and translated at render time, so switching
// the language also re-translates messages that are already on screen.
type ListState =
  | { status: 'loading'; page: ExpensePage | null }
  | { status: 'ready'; page: ExpensePage }
  | { status: 'error'; error: unknown };

type Banner = { kind: 'alreadyDeleted' } | { kind: 'deleteFailed'; error: unknown } | null;

// `null` = dialog closed; `{ expense: null }` = create; otherwise edit.
type DialogState = { expense: Expense | null } | null;

export function App() {
  const { m, money, date } = useI18n();
  const [config, setConfig] = useState<AppConfig | null>(null);
  const [configError, setConfigError] = useState<unknown>(null);
  const [filters, setFilters] = useState<Filters | null>(null);
  const [offset, setOffset] = useState(0);
  const [list, setList] = useState<ListState>({ status: 'loading', page: null });
  const [reloadKey, setReloadKey] = useState(0);
  const [banner, setBanner] = useState<Banner>(null);
  const [dialog, setDialog] = useState<DialogState>(null);

  useEffect(() => {
    api
      .config()
      .then((c) => {
        setConfig(c);
        const [from, to] = presetRange('this_month', c.today);
        setFilters({ userId: '', category: '', from, to });
      })
      .catch((e) => setConfigError(e ?? new Error()));
  }, []);

  // Load the list whenever filters, page or reloadKey change. Superseded
  // requests are aborted so stale responses never overwrite newer data.
  useEffect(() => {
    if (!filters || hasRangeError(filters)) return;
    const ctrl = new AbortController();
    setList((prev) => ({ status: 'loading', page: prev.status === 'error' ? null : prev.page }));
    api
      .list(filters, PAGE_SIZE, offset, ctrl.signal)
      .then((page) => setList({ status: 'ready', page }))
      .catch((error) => {
        if (ctrl.signal.aborted) return;
        setList({ status: 'error', error });
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
      m.remove.confirm(money(e.amount, config.currency), m.categories[e.category], date(e.spent_at), e.member_name),
    );
    if (!ok) return;
    try {
      await api.remove(e.id);
      setBanner(null);
      const page = list.status === 'ready' ? list.page : null;
      if (page && page.items.length === 1 && offset > 0) setOffset(Math.max(0, offset - PAGE_SIZE));
    } catch (error) {
      setBanner(error instanceof ApiError && error.kind === 'notFound' ? { kind: 'alreadyDeleted' } : { kind: 'deleteFailed', error });
    }
    reload();
  }

  if (configError !== null) {
    return (
      <Shell canAdd={false} onAdd={() => {}}>
        <div className="state error">{m.list.configFailed(describeError(configError, m))}</div>
      </Shell>
    );
  }
  if (!config || !filters) {
    return (
      <Shell canAdd={false} onAdd={() => {}}>
        <div className="state">{m.list.loading}</div>
      </Shell>
    );
  }

  const page = list.status === 'error' ? null : list.page;
  const pages = page ? Math.max(1, Math.ceil(page.total / PAGE_SIZE)) : 1;
  const bannerText =
    banner?.kind === 'alreadyDeleted'
      ? m.remove.alreadyDeleted
      : banner?.kind === 'deleteFailed'
        ? m.remove.failed(describeError(banner.error, m))
        : null;

  return (
    <Shell canAdd onAdd={() => setDialog({ expense: null })}>
      {bannerText && (
        <div className="banner" role="alert">
          {bannerText}
        </div>
      )}
      <FiltersBar config={config} filters={filters} onChange={changeFilters} />

      <section className="summary" aria-live="polite">
        {page ? m.list.summary(page.total, money(page.sum, config.currency)) : '—'}
      </section>

      {list.status === 'error' && <div className="state error">{m.list.loadFailed(describeError(list.error, m))}</div>}
      {list.status === 'loading' && !page && <div className="state">{m.list.loading}</div>}
      {page && page.items.length === 0 && list.status === 'ready' && <div className="state empty">{m.list.empty}</div>}
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
            {m.list.prev}
          </button>
          <span>{m.list.page(Math.floor(offset / PAGE_SIZE) + 1, pages)}</span>
          <button
            type="button"
            className="btn small"
            disabled={offset + PAGE_SIZE >= page.total}
            onClick={() => setOffset(offset + PAGE_SIZE)}
          >
            {m.list.next}
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
  const { m } = useI18n();
  return (
    <>
      <header className="topbar">
        <h1>{m.header.title}</h1>
        <div className="topbar-actions">
          <LanguageSwitcher />
          <button type="button" className="btn primary" disabled={!canAdd} onClick={onAdd}>
            {m.header.add}
          </button>
        </div>
      </header>
      <main>{children}</main>
    </>
  );
}
