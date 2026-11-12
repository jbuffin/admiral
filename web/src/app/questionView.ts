import type { Item } from './api';

export interface OptionView {
  label: string;
  description: string;
  chosen: boolean;
}

export interface QuestionView {
  id: string;
  header: string;
  text: string;
  multiSelect: boolean;
  pending: boolean;
  options: OptionView[];
  /** What the answer holds beyond the listed options: Other's text. */
  freeText?: string;
}

// Claude records one string per question: a multi-select's labels joined by ", ", or Other's text. A label may hold ", " itself.
function split(answer: string, labels: string[], several: boolean): { chosen: Set<string>; rest: string[] } {
  const chosen = new Set<string>();
  if (!several) return labels.includes(answer) ? { chosen: new Set([answer]), rest: [] } : { chosen, rest: [answer] };
  const parts = answer.split(', ');
  const rest: string[] = [];
  for (let i = 0; i < parts.length; ) {
    let hit = 0;
    for (let n = parts.length - i; n >= 1 && !hit; n--) if (labels.includes(parts.slice(i, i + n).join(', '))) hit = n;
    if (hit) chosen.add(parts.slice(i, i + hit).join(', '));
    else rest.push(parts[i]);
    i += hit || 1;
  }
  return { chosen, rest };
}

/** The questions of one call as the transcript shows them: what is asked, what is chosen, and any free-text answer. */
export function questionCallView(items: Item[]): QuestionView[] {
  return items.flatMap((item) => {
    const q = item.question;
    if (!q) return [];
    const labels = (q.options ?? []).map((o) => o.label);
    const { chosen, rest } = q.answer ? split(q.answer, labels, q.multiSelect === true) : { chosen: new Set<string>(), rest: [] };
    return [
      {
        id: item.id,
        header: q.header ?? '',
        text: q.text,
        multiSelect: q.multiSelect === true,
        pending: q.pending,
        options: (q.options ?? []).map((o) => ({ label: o.label, description: o.description ?? '', chosen: chosen.has(o.label) })),
        ...(rest.length > 0 ? { freeText: rest.join(', ') } : {}),
      },
    ];
  });
}
