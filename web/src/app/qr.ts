import { encode } from 'uqr';

/** The QR modules for a text, true = dark. Nothing leaves the browser. */
export const qrMatrix = (text: string): boolean[][] => encode(text, { ecc: 'M', border: 0 }).data;
