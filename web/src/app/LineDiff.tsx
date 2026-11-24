export interface DiffLine {
  kind: 'same' | 'add' | 'del';
  text: string;
}

// diffLines is the line-by-line difference from the published text to the new one.
export function diffLines(_before: string, _after: string): DiffLine[] {
  return [];
}

export function LineDiff(_props: { before: string; after: string }) {
  return null;
}
