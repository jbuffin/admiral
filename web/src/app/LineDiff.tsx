export interface DiffLine {
  kind: 'same' | 'add' | 'del';
  text: string;
}

const linesOf = (text: string) => (text === '' ? [] : text.replace(/\n$/, '').split('\n'));

// diffLines is the line-by-line difference from the published text to the new one.
export function diffLines(before: string, after: string): DiffLine[] {
  const a = linesOf(before);
  const b = linesOf(after);
  // common[i][j] is the length of the longest common run of a[i:] and b[j:].
  const common = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) common[i][j] = a[i] === b[j] ? common[i + 1][j + 1] + 1 : Math.max(common[i + 1][j], common[i][j + 1]);
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      out.push({ kind: 'same', text: a[i++] });
      j++;
    } else if (j === b.length || (i < a.length && common[i + 1][j] >= common[i][j + 1])) out.push({ kind: 'del', text: a[i++] });
    else out.push({ kind: 'add', text: b[j++] });
  }
  return out;
}

const MARK = { same: ' ', del: '-', add: '+' } as const;

export function LineDiff({ before, after }: { before: string; after: string }) {
  return (
    <pre class="line-diff">
      {diffLines(before, after).map((l, i) => (
        <div key={i} data-kind={l.kind} class={`diff-${l.kind}`}>
          {MARK[l.kind]} {l.text}
        </div>
      ))}
    </pre>
  );
}
