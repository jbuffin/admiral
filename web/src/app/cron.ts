const DAYS = new Set(['1-5', 'mon-fri']);
const num = (s: string, max: number) => (/^\d{1,2}$/.test(s) && Number(s) <= max ? String(Number(s)).padStart(2, '0') : null);

/** A cron expression as a plain sentence ("Weekdays at 08:30"), or the expression itself when no sentence fits. */
export function cronSentence(expr: string): string {
  const f = expr.trim().split(/\s+/);
  if (f.length !== 5) return expr;
  const [m, h, dom, mon, dow] = f;
  const mm = num(m, 59);
  const hh = num(h, 23);
  if (mm === null || hh === null || dom !== '*' || mon !== '*') return expr;
  if (dow === '*') return `Daily ${hh}:${mm}`;
  return DAYS.has(dow.toLowerCase()) ? `Weekdays at ${hh}:${mm}` : expr;
}
