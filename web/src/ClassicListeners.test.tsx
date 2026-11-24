import { render } from '@testing-library/preact';
import { expect, it, vi } from 'vitest';

vi.mock('./connection', async (original) => ({ ...(await original<typeof import('./connection')>()), connect: () => () => {} }));

it('the classic view listens for no key, press or tap on the window or document', async () => {
  const keys = new Set(['keydown', 'keyup', 'pointerdown', 'pointerup', 'click', 'touchstart', 'mousedown']);
  const heard: string[] = [];
  for (const target of [window, document]) {
    const add = target.addEventListener.bind(target);
    vi.spyOn(target, 'addEventListener').mockImplementation(((type: string, ...rest: unknown[]) => {
      if (keys.has(type)) heard.push(type);
      return (add as (...a: unknown[]) => void)(type, ...rest);
    }) as typeof target.addEventListener);
  }
  const { App } = await import('./App');
  render(<App />);
  expect(heard).toEqual([]);
});
