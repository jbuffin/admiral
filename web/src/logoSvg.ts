// Reads an official logo file into what a <symbol> needs: its viewBox (single
// or double quotes) and its inner markup.
export function parseLogoSvg(svg: string | undefined): { viewBox: string; inner: string } | undefined {
  const m = svg?.match(/<svg\b[^>]*?\bviewBox\s*=\s*(["'])([^"']+)\1[^>]*>([\s\S]*)<\/svg>/);
  return m ? { viewBox: m[2], inner: m[3] } : undefined;
}
