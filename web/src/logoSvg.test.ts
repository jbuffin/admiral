import { expect, test } from 'vitest';
import { parseLogoSvg } from './logoSvg';

test('reads the viewBox in either quote style, and the inner markup', () => {
  const inner = '<rect width="10" height="10"/>';
  expect(parseLogoSvg(`<svg xmlns="x" viewBox="0 0 10 10">${inner}</svg>`)).toEqual({ viewBox: '0 0 10 10', inner });
  expect(parseLogoSvg(`<svg viewBox='0 0 24 24' width='24'>${inner}</svg>`)).toEqual({ viewBox: '0 0 24 24', inner });
  expect(parseLogoSvg('<svg width="10"></svg>')).toBeUndefined();
});
