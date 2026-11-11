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
});
