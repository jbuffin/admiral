import { describe, expect, it } from 'vitest';
import { splitDiff } from './changes';

describe('splitDiff', () => {
  it('splits a diff into its files and each file into its hunks', () => {
    const diff = [
      'diff --git a/a.txt b/a.txt',
      'index 1111111..2222222 100644',
      '--- a/a.txt',
      '+++ b/a.txt',
      '@@ -1,3 +1,3 @@',
      ' one',
      '-two',
      '+TWO',
      ' three',
      '@@ -10,2 +10,3 @@ func later()',
      ' ten',
      '+ten and a half',
      ' eleven',
      'diff --git a/b.txt b/b.txt',
      '--- a/b.txt',
      '+++ b/b.txt',
      '@@ -1 +1 @@',
      '-old',
      '+new',
      '',
    ].join('\n');
    expect(splitDiff(diff)).toEqual([
      {
        path: 'a.txt',
        hunks: [
          { header: '@@ -1,3 +1,3 @@', lines: [' one', '-two', '+TWO', ' three'] },
          { header: '@@ -10,2 +10,3 @@ func later()', lines: [' ten', '+ten and a half', ' eleven'] },
        ],
      },
      { path: 'b.txt', hunks: [{ header: '@@ -1 +1 @@', lines: ['-old', '+new'] }] },
    ]);
  });

  it('reads an untracked file as a new file under its own path', () => {
    const diff = ['diff --git a/c.txt b/c.txt', 'new file mode 100644', 'index 0000000..e69de29', '--- /dev/null', '+++ b/c.txt', '@@ -0,0 +1 @@', '+loose', ''].join('\n');
    expect(splitDiff(diff)).toEqual([{ path: 'c.txt', hunks: [{ header: '@@ -0,0 +1 @@', lines: ['+loose'] }] }]);
  });

  it('names a deleted file by the path it had', () => {
    const diff = ['diff --git a/gone.txt b/gone.txt', 'deleted file mode 100644', 'index 2222222..0000000', '--- a/gone.txt', '+++ /dev/null', '@@ -1 +0,0 @@', '-bye', ''].join('\n');
    expect(splitDiff(diff).map((f) => f.path)).toEqual(['gone.txt']);
  });

  it('names a renamed file by its new path, with no hunks when its content is the same', () => {
    const diff = ['diff --git a/old.txt b/new.txt', 'similarity index 100%', 'rename from old.txt', 'rename to new.txt', ''].join('\n');
    expect(splitDiff(diff)).toEqual([{ path: 'new.txt', hunks: [] }]);
  });

  it('gives a binary file no hunks', () => {
    const diff = ['diff --git a/logo.png b/logo.png', 'index 1111111..2222222 100644', 'Binary files a/logo.png and b/logo.png differ', ''].join('\n');
    expect(splitDiff(diff)).toEqual([{ path: 'logo.png', hunks: [] }]);
  });

  it('keeps a removed line that reads like a file marker as a line of its hunk', () => {
    const diff = ['diff --git a/s.sql b/s.sql', '--- a/s.sql', '+++ b/s.sql', '@@ -1,2 +1,2 @@', ' keep', '--- x', '+++ y', 'diff --git a/t.txt b/t.txt', '--- a/t.txt', '+++ b/t.txt', '@@ -1 +1 @@', '-a', '+b', ''].join('\n');
    const files = splitDiff(diff);
    expect(files.map((f) => f.path)).toEqual(['s.sql', 't.txt']);
    expect(files[0].hunks[0].lines).toEqual([' keep', '--- x', '+++ y']);
  });

  it('keeps what arrived of a diff cut in the middle of a hunk', () => {
    const diff = ['diff --git a/big.txt b/big.txt', '--- a/big.txt', '+++ b/big.txt', '@@ -1,5 +1,5 @@', ' one', '-two'].join('\n');
    expect(splitDiff(diff)).toEqual([{ path: 'big.txt', hunks: [{ header: '@@ -1,5 +1,5 @@', lines: [' one', '-two'] }] }]);
  });

  it('reads an empty diff as no files', () => {
    expect(splitDiff('')).toEqual([]);
  });

  it('keeps a no-newline marker as a line of its hunk', () => {
    const diff = ['diff --git a/n.txt b/n.txt', '--- a/n.txt', '+++ b/n.txt', '@@ -1 +1 @@', '-old', '\\ No newline at end of file', '+new', '\\ No newline at end of file', ''].join('\n');
    expect(splitDiff(diff)).toEqual([{ path: 'n.txt', hunks: [{ header: '@@ -1 +1 @@', lines: ['-old', '\\ No newline at end of file', '+new', '\\ No newline at end of file'] }] }]);
  });

  it('names a renamed file with edits by its new path and keeps its hunk', () => {
    const diff = ['diff --git a/old.txt b/new.txt', 'similarity index 80%', 'rename from old.txt', 'rename to new.txt', 'index 1111111..2222222 100644', '--- a/old.txt', '+++ b/new.txt', '@@ -1,2 +1,2 @@', ' keep', '-was', '+now', ''].join('\n');
    expect(splitDiff(diff)).toEqual([{ path: 'new.txt', hunks: [{ header: '@@ -1,2 +1,2 @@', lines: [' keep', '-was', '+now'] }] }]);
  });

  it('keeps the spaces of a path', () => {
    const diff = ['diff --git a/my notes/a b.txt b/my notes/a b.txt', '--- a/my notes/a b.txt\t', '+++ b/my notes/a b.txt\t', '@@ -1 +1 @@', '-x', '+y', ''].join('\n');
    expect(splitDiff(diff).map((f) => f.path)).toEqual(['my notes/a b.txt']);
  });

  it('unquotes a path that git quoted', () => {
    const diff = [
      'diff --git "a/tab\\there.txt" "b/tab\\there.txt"',
      '--- "a/tab\\there.txt"',
      '+++ "b/tab\\there.txt"',
      '@@ -1 +1 @@',
      '-x',
      '+y',
      'diff --git "a/caf\\303\\251 \\"q\\".txt" "b/caf\\303\\251 \\"q\\".txt"',
      'new file mode 100644',
      'Binary files /dev/null and "b/caf\\303\\251 \\"q\\".txt" differ',
      'diff --git "a/old\\\\x.txt" "b/new\\nline.txt"',
      'rename from "old\\\\x.txt"',
      'rename to "new\\nline.txt"',
      '',
    ].join('\n');
    expect(splitDiff(diff).map((f) => f.path)).toEqual(['tab\there.txt', 'café "q".txt', 'new\nline.txt']);
  });

  it('gives a type change its two sections under the same path', () => {
    const diff = ['diff --git a/l b/l', 'deleted file mode 120000', '--- a/l', '+++ /dev/null', '@@ -1 +0,0 @@', '-target', '\\ No newline at end of file', 'diff --git a/l b/l', 'new file mode 100644', '--- /dev/null', '+++ b/l', '@@ -0,0 +1 @@', '+text', ''].join('\n');
    const files = splitDiff(diff);
    expect(files.map((f) => f.path)).toEqual(['l', 'l']);
    expect(files.map((f) => f.hunks[0].lines[0])).toEqual(['-target', '+text']);
  });
});
