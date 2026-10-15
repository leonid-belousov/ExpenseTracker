// Calendar dates are "YYYY-MM-DD" strings. Arithmetic is done in UTC so the
// viewer's own time zone and DST never shift a date. "Today" always comes
// from the backend, which knows the family's configured time zone.

function parse(s: string): Date {
  const [y, m, d] = s.split('-').map(Number);
  return new Date(Date.UTC(y, m - 1, d));
}

function format(d: Date): string {
  return d.toISOString().slice(0, 10);
}

export function addDays(s: string, n: number): string {
  const d = parse(s);
  d.setUTCDate(d.getUTCDate() + n);
  return format(d);
}

export function humanDate(s: string): string {
  const [y, m, d] = s.split('-');
  return `${d}.${m}.${y}`;
}

export type Preset = 'this_week' | 'this_month' | 'last_month' | 'all';

/** Returns an inclusive [from, to] pair for the dashboard date inputs.
 * Weeks start on Monday, matching the backend and the bot. */
export function presetRange(preset: Preset, today: string): [string, string] {
  const t = parse(today);
  const y = t.getUTCFullYear();
  const m = t.getUTCMonth();
  switch (preset) {
    case 'this_week': {
      const offset = (t.getUTCDay() + 6) % 7; // Monday = 0
      const from = addDays(today, -offset);
      return [from, addDays(from, 6)];
    }
    case 'this_month':
      return [format(new Date(Date.UTC(y, m, 1))), format(new Date(Date.UTC(y, m + 1, 0)))];
    case 'last_month':
      return [format(new Date(Date.UTC(y, m - 1, 1))), format(new Date(Date.UTC(y, m, 0)))];
    case 'all':
      return ['', ''];
  }
}
