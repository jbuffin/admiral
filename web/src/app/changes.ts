export interface DiffHunk {
  header: string;
  lines: string[];
}
export interface DiffFile {
  path: string;
  hunks: DiffHunk[];
}

const HEADER = /^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@/;

// `diff --git a/P b/P`: a path with no rename and no ---/+++ lines (binary, mode change) only appears here.
function pathFromGitLine(rest: string): string {
  const same = (rest.length - 5) / 2;
  if (rest.startsWith('a/') && Number.isInteger(same) && rest.slice(same + 2, same + 5) === ' b/' && rest.slice(2, same + 2) === rest.slice(same + 5)) return rest.slice(2, same + 2);
  const at = rest.lastIndexOf(' b/');
  return at < 0 ? rest : rest.slice(at + 3);
}

const unprefix = (p: string) => p.replace(/\t.*$/, '').replace(/^[ab]\//, '');

/** Splits a unified diff into its files and their hunks. A hunk's own line counts say where it ends, so a removed line reading `--- x` stays in it. */
export function splitDiff(diff: string): DiffFile[] {
  const files: DiffFile[] = [];
  let file: DiffFile | undefined;
  let hunk: DiffHunk | undefined;
  let oldLeft = 0;
  let newLeft = 0;
  let named = false;
  for (const line of diff.split('\n')) {
    if (hunk && (oldLeft > 0 || newLeft > 0)) {
      const c = line[0];
      if (c === ' ' || (c === '-' && oldLeft > 0) || (c === '+' && newLeft > 0) || line === '') {
        // An empty line is a context line whose leading space was stripped.
        if (c !== '+') oldLeft--;
        if (c !== '-') newLeft--;
        hunk.lines.push(line === '' ? ' ' : line);
        continue;
      }
      if (c === '\\') {
        hunk.lines.push(line);
        continue;
      }
    }
    if (line.startsWith('diff --git ')) {
      file = { path: pathFromGitLine(line.slice(11)), hunks: [] };
      files.push(file);
      hunk = undefined;
      named = false;
    } else if (!file) {
      continue;
    } else if (line.startsWith('rename to ')) {
      file.path = line.slice(10);
      named = true;
    } else if (!hunk && line.startsWith('+++ ') && !named) {
      if (line !== '+++ /dev/null') file.path = unprefix(line.slice(4));
      named = true;
    } else if (!hunk && line.startsWith('--- ') && !named) {
      // A deleted file's +++ is /dev/null, so it keeps the path it had.
      if (line !== '--- /dev/null') file.path = unprefix(line.slice(4));
    } else {
      const m = HEADER.exec(line);
      if (m) {
        hunk = { header: line, lines: [] };
        file.hunks.push(hunk);
        oldLeft = m[1] === undefined ? 1 : Number(m[1]);
        newLeft = m[2] === undefined ? 1 : Number(m[2]);
      } else if (hunk && line === '\\ No newline at end of file') hunk.lines.push(line);
    }
  }
  return files;
}
