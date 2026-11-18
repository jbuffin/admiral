import { render } from '@testing-library/preact';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import type { Order } from './api';
import { OrderPane } from './OrderPane';
import type { OrderRow } from './ordersView';

beforeEach(() => vi.stubGlobal('fetch', vi.fn(async () => new Response('{}'))));
afterEach(() => vi.unstubAllGlobals());

const pane = (excerpt: string) => {
  const order = { id: 'acme/api#7', title: 'Fix it', url: 'https://github.com/acme/api/pull/7', excerpt } as Order;
  const row = { key: 'gh:acme/api#7', source: 'gh', order, reason: '', state: '', needsYou: false, inView: true, launchable: false } as OrderRow;
  const { container } = render(<OrderPane row={row} sources={[]} now={Date.now()} heading={false} onLaunch={() => undefined} />);
  return container.querySelector('.odetail-excerpt') as HTMLElement;
};

test('the excerpt renders inline markdown', () => {
  const el = pane('Adds **bold** and `code` here');
  expect(el.querySelector('strong')?.textContent).toBe('bold');
  expect(el.querySelector('code')?.textContent).toBe('code');
  expect(el.textContent).toBe('Adds bold and code here');
});

test('the excerpt drops HTML comments and keeps raw HTML as text', () => {
  const el = pane('<!-- marker -->See <b>this</b> <details>');
  expect(el.querySelector('b')).toBeNull();
  expect(el.querySelector('details')).toBeNull();
  expect(el.textContent).toContain('<b>this</b>');
  expect(el.textContent).not.toContain('marker');
});
