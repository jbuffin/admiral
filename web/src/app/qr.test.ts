import jsQR from 'jsqr';
import { describe, expect, it } from 'vitest';
import { qrMatrix } from './qr';

// Paints the matrix the way a phone camera sees it: dark modules on white with a quiet zone.
function decode(dark: boolean[][]) {
  const scale = 4;
  const quiet = 4;
  const side = (dark.length + 2 * quiet) * scale;
  const px = new Uint8ClampedArray(side * side * 4).fill(255);
  dark.forEach((row, y) =>
    row.forEach((on, x) => {
      if (!on) return;
      for (let dy = 0; dy < scale; dy++) {
        for (let dx = 0; dx < scale; dx++) {
          const i = (((y + quiet) * scale + dy) * side + (x + quiet) * scale + dx) * 4;
          px[i] = px[i + 1] = px[i + 2] = 0;
        }
      }
    }),
  );
  return jsQR(px, side, side)?.data;
}

describe('qrMatrix', () => {
  it.each([
    'http://192.168.1.20:7420/pair?code=ABCD-2345',
    'https://mac.tail1234.ts.net/pair?code=WXYZ-7890',
  ])('draws a code a scanner reads back as %s', (url) => {
    expect(decode(qrMatrix(url))).toBe(url);
  });
});
