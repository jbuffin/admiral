import cronstrue from 'cronstrue';

/** What the When field says: a plain-English description, or the plain reason Admiral's scheduler would refuse it. */
export type CronDescription = { ok: true; text: string } | { ok: false; reason: string };

interface Spec {
  name: string;
  lo: number;
  hi: number;
  names?: string[];
}

// Mirrors cronFields in internal/routines/cron.go.
const FIELDS: Spec[] = [
  { name: 'minute', lo: 0, hi: 59 },
  { name: 'hour', lo: 0, hi: 23 },
  { name: 'day of month', lo: 1, hi: 31 },
  { name: 'month', lo: 1, hi: 12, names: ['jan', 'feb', 'mar', 'apr', 'may', 'jun', 'jul', 'aug', 'sep', 'oct', 'nov', 'dec'] },
  { name: 'day of week', lo: 0, hi: 7, names: ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'] },
];
const FIELD_HEAD = 'minute hour day-of-month month day-of-week';
const NEVER = 'This schedule never fires: no day in the next five years matches.';
const MAX_STEP = 9223372036854775807n;

class Refused extends Error {}

// Go's strconv.Atoi: an optional sign, then digits.
function atoi(s: string): number | null {
  if (!/^[+-]?\d+$/.test(s)) return null;
  const big = BigInt(s);
  if (big > MAX_STEP || big < -MAX_STEP - 1n) return null;
  return Number(big);
}

// shown is what the engineer typed, so a leading sign (which Go sees as an empty piece) is named whole.
function value(s: string, f: Spec, shown = s): number {
  const at = f.names?.findIndex((n) => n === s.toLowerCase()) ?? -1;
  if (at >= 0) return f.lo + at;
  const n = atoi(s);
  if (n === null || s[0] === '+' || s[0] === '-') throw new Refused(`${s === '' ? shown : s} is not a number`);
  if (n < f.lo || n > f.hi) throw new Refused(`${n} is outside ${f.lo}-${f.hi}`);
  return n;
}

interface Parsed {
  values: Set<number>;
  items: string[]; // each item as numbers only, in the form cronstrue reads the way Go runs it
}

function parseField(s: string, f: Spec): Parsed {
  const values = new Set<number>();
  const items: string[] = [];
  const dow = f.name === 'day of week';
  for (const item of s.split(',')) {
    const slash = item.indexOf('/');
    const rng = slash < 0 ? item : item.slice(0, slash);
    const hasStep = slash >= 0;
    let step = 1;
    if (hasStep) {
      const stepText = item.slice(slash + 1);
      const n = atoi(stepText);
      if (n === null || n < 1) throw new Refused(`${stepText} is not a valid step, use a whole number of 1 or more`);
      step = n;
    }
    let lo = f.lo;
    let hi = dow ? 6 : f.hi; // a bare * is Sunday to Saturday; 7 only as an explicit value
    const star = rng === '*';
    let isRange = false;
    if (!star) {
      const dash = rng.indexOf('-');
      isRange = dash >= 0;
      lo = value(isRange ? rng.slice(0, dash) : rng, f, rng);
      hi = lo;
      if (isRange) hi = value(rng.slice(dash + 1), f, rng);
      else if (hasStep) hi = f.hi;
      if (lo > hi) throw new Refused(`${rng} runs backwards`);
    }
    for (let v = lo; v <= hi; v += step) values.add(v);
    const steps = [];
    for (let v = lo; v <= hi; v += step) steps.push(v);
    // Go restarts a star step at the field's low each cycle, so only a step that divides the span is an even interval.
    if (star && (!hasStep || (f.hi - f.lo + 1) % step === 0)) items.push(hasStep ? `*/${step}` : '*');
    else if (hasStep && isRange) items.push(`${lo}-${hi}/${step}`);
    else if (!hasStep) items.push(lo === hi ? String(lo) : `${lo}-${hi}`);
    else items.push(...runsOf(steps));
  }
  if (dow && values.has(0)) values.add(7);
  if (dow && values.has(7)) values.add(0);
  return { values, items };
}

function runsOf(sorted: number[]): string[] {
  const runs: string[] = [];
  for (let i = 0; i < sorted.length; ) {
    let j = i;
    while (j + 1 < sorted.length && sorted[j + 1] === sorted[j] + 1) j++;
    runs.push(j > i ? `${sorted[i]}-${sorted[j]}` : String(sorted[i]));
    i = j + 1;
  }
  return runs;
}

// Sunday as 7 and the days as runs, so cronstrue says "Friday through Sunday" for 5-7. With `listed`, one day per item:
// cronstrue drops its "and on" marker for a weekday range, which would read an either-day match as both.
function weekdayItems(values: Set<number>, listed: boolean): string {
  const days = [...values].filter((v) => v !== 0).sort((a, b) => a - b);
  return (listed ? days.map(String) : runsOf(days)).join(',');
}

function neverFires(month: Set<number>, dom: Set<number>, dow: Set<number>, and: boolean, now: Date): boolean {
  const day = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
  const last = new Date(now);
  last.setUTCFullYear(last.getUTCFullYear() + 5);
  for (; day <= last; day.setUTCDate(day.getUTCDate() + 1)) {
    if (!month.has(day.getUTCMonth() + 1)) continue;
    const inDom = dom.has(day.getUTCDate());
    const inDow = dow.has(day.getUTCDay());
    if (and ? inDom && inDow : inDom || inDow) return false;
  }
  return true;
}

/** Describes `expr` as Admiral's scheduler reads it, naming `zone` when known; `now` is the start of the never-fires window. */
export function describeCron(expr: string, zone?: string, now: Date = new Date()): CronDescription {
  const parts = expr.split(/\s+/).filter(Boolean);
  if (parts.length === 0) return { ok: false, reason: `When is empty: write five fields, minute hour day-of-month month day-of-week.` };
  if (parts.length !== 5) return { ok: false, reason: `When needs 5 fields (${FIELD_HEAD}) but has ${parts.length}.` };
  const parsed: Parsed[] = [];
  for (let i = 0; i < 5; i++) {
    try {
      parsed.push(parseField(parts[i], FIELDS[i]));
    } catch (e) {
      if (e instanceof Refused) return { ok: false, reason: `${FIELDS[i].name}: ${e.message}` };
      throw e;
    }
  }
  const and = parts[2].startsWith('*') || parts[4].startsWith('*');
  if (neverFires(parsed[3].values, parsed[2].values, parsed[4].values, and, now)) return { ok: false, reason: NEVER };
  const normal = [parsed[0].items.join(','), parsed[1].items.join(','), parsed[2].items.join(','), parsed[3].items.join(','), parts[4] === '*' ? '*' : weekdayItems(parsed[4].values, !and)].join(' ');
  let text: string;
  try {
    text = cronstrue.toString(normal, { use24HourTimeFormat: true, logicalAndDayFields: and, throwExceptionOnParseError: true });
  } catch {
    text = parts.join(' ');
  }
  return { ok: true, text: zone ? `${text} (${zone})` : text };
}
