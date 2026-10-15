import { presetRange, type Preset } from '../dates';
import { CATEGORY_LABELS } from '../format';
import type { AppConfig, Filters } from '../types';

interface Props {
  config: AppConfig;
  filters: Filters;
  onChange: (f: Filters) => void;
}

const PRESETS: [Preset, string][] = [
  ['this_week', 'Эта неделя'],
  ['this_month', 'Этот месяц'],
  ['last_month', 'Прошлый месяц'],
  ['all', 'Всё время'],
];

export function rangeError(f: Filters): string | null {
  return f.from && f.to && f.to < f.from ? 'Дата «по» не может быть раньше даты «с».' : null;
}

export function FiltersBar({ config, filters, onChange }: Props) {
  const set = (patch: Partial<Filters>) => onChange({ ...filters, ...patch });
  const error = rangeError(filters);

  return (
    <form className="filters" autoComplete="off" onSubmit={(e) => e.preventDefault()}>
      <label>
        Член семьи
        <select value={filters.userId} onChange={(e) => set({ userId: e.target.value })}>
          <option value="">Все</option>
          {config.members.map((m) => (
            <option key={m.id} value={String(m.id)}>
              {m.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        Категория
        <select value={filters.category} onChange={(e) => set({ category: e.target.value })}>
          <option value="">Все</option>
          {config.categories.map((c) => (
            <option key={c} value={c}>
              {CATEGORY_LABELS[c]}
            </option>
          ))}
        </select>
      </label>
      <label>
        С даты
        <input type="date" value={filters.from} onChange={(e) => set({ from: e.target.value })} />
      </label>
      <label>
        По дату (включительно)
        <input type="date" value={filters.to} onChange={(e) => set({ to: e.target.value })} />
      </label>
      <div className="presets">
        {PRESETS.map(([p, label]) => (
          <button
            key={p}
            type="button"
            className="btn small"
            onClick={() => {
              const [from, to] = presetRange(p, config.today);
              set({ from, to });
            }}
          >
            {label}
          </button>
        ))}
      </div>
      {error && <p className="field-error filters-error">{error}</p>}
    </form>
  );
}
