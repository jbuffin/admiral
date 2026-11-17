import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { checkBuild } from './buildCheck';

const built = { version: '1.4.0', commit: 'abc1234' };
const stamp = (content: string) => {
  const meta = document.createElement('meta');
  meta.name = 'admiral-build';
  meta.content = content;
  document.head.append(meta);
};
const serverRuns = (b: { version: string; commit: string }) => vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(b), { status: 200 })));
let reload: ReturnType<typeof vi.fn>;

beforeEach(() => {
  reload = vi.fn();
  vi.stubGlobal('location', { ...window.location, reload });
  sessionStorage.clear();
});
afterEach(() => {
  vi.unstubAllGlobals();
  document.querySelector('meta[name="admiral-build"]')?.remove();
  sessionStorage.clear();
});

test('the shell reloads once when the server runs another build, and not again for that build', async () => {
  stamp('1.4.0 abc1234');
  serverRuns({ version: '1.5.0', commit: 'def5678' });
  await checkBuild();
  expect(reload).toHaveBeenCalledTimes(1);
  await checkBuild();
  expect(reload).toHaveBeenCalledTimes(1);
});

test("the shell stays when the server runs the shell's own build", async () => {
  stamp('1.4.0 abc1234');
  serverRuns(built);
  await checkBuild();
  expect(reload).not.toHaveBeenCalled();
});

test('a page with no stamp asks nothing of the server', async () => {
  serverRuns({ version: '1.5.0', commit: 'def5678' });
  await checkBuild();
  expect(fetch).not.toHaveBeenCalled();
  expect(reload).not.toHaveBeenCalled();
});
