import { presetRange, type Preset } from '../dates';
import { useI18n } from '../i18n/I18nProvider';
import type { Messages } from '../i18n/messages';
import type { AppConfig, Filters } from '../types';

interface Props {
  config: AppConfig;
  filters: Filters;
  onChange: (f: Filters) => void;
}

const PRESETS: [Preset, keyof Messages['filters']][] = [
  ['this_week', 'thisWeek'],
  ['this_month', 'thisMonth'],
  ['last_month', 'lastMonth'],
  ['all', 'allTime'],
];

/** True when the inclusive range is reversed; the list is not loaded then. */
export function hasRangeError(f: Filters): boolean {
  return Boolean(f.from && f.to && f.to < f.from);
}

export function FiltersBar({ config, filters, onChange }: Props) {
  const { m } = useI18n();
  const set = (patch: Partial<Filters>) => onChange({ ...filters, ...patch });

  return (
    <form className="filters" autoComplete="off" onSubmit={(e) => e.preventDefault()}>
      <label>
        {m.filters.member}
        <select value={filters.userId} onChange={(e) => set({ userId: e.target.value })}>
          <option value="">{m.filters.all}</option>
          {config.members.map((mem) => (
            <option key={mem.id} value={String(mem.id)}>
              {mem.name}
            </option>
          ))}
        </select>
      </label>
      <label>
        {m.filters.category}
        <select value={filters.category} onChange={(e) => set({ category: e.target.value })}>
          <option value="">{m.filters.all}</option>
          {config.categories.map((c) => (
            <option key={c} value={c}>
              {m.categories[c]}
            </option>
          ))}
        </select>
      </label>
      <label>
        {m.filters.from}
        <input type="date" value={filters.from} onChange={(e) => set({ from: e.target.value })} />
      </label>
      <label>
        {m.filters.to}
        <input type="date" value={filters.to} onChange={(e) => set({ to: e.target.value })} />
      </label>
      <div className="presets">
        {PRESETS.map(([p, key]) => (
          <button
            key={p}
            type="button"
            className="btn small"
            onClick={() => {
              const [from, to] = presetRange(p, config.today);
              set({ from, to });
            }}
          >
            {m.filters[key]}
          </button>
        ))}
      </div>
      {hasRangeError(filters) && <p className="field-error filters-error">{m.filters.rangeError}</p>}
    </form>
  );
}
