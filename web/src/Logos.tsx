import { harnesses, logoId } from './harnesses';
import type { HarnessName } from './config';

// Official logo files, when the engineer has put them in web/logos/official/
// (gitignored; see the README). Each is named for its harness and carries its
// own field, because the vendors' usage rules set the field.
const official = import.meta.glob('/logos/official/*.svg', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>;

function officialSymbol(h: HarnessName): { viewBox: string; inner: string } | undefined {
  const svg = official[`/logos/official/${h}.svg`];
  const m = svg?.match(/<svg\b[^>]*\bviewBox="([^"]+)"[^>]*>([\s\S]*)<\/svg>/);
  return m ? { viewBox: m[1], inner: m[2] } : undefined;
}

// The flag draws a logo by id, so swapping logos changes only these symbols.
// The placeholders are the lettered set: a field in the harness flag color.
function Symbol({ harness }: { harness: HarnessName }) {
  const o = officialSymbol(harness);
  if (o) return <symbol id={logoId(harness)} viewBox={o.viewBox} dangerouslySetInnerHTML={{ __html: o.inner }} />;
  const letters = { claude: 'Cl', copilot: 'Co', codex: 'Cx' }[harness];
  return (
    <symbol id={logoId(harness)} viewBox="0 0 40 28" preserveAspectRatio="xMidYMid slice">
      <rect width="40" height="28" style={{ fill: `var(--harness-${harness}-flag)` }} />
      <text
        x="20"
        y="19"
        text-anchor="middle"
        font-family="Bricolage Grotesque, sans-serif"
        font-weight="800"
        font-size="13"
        style={{ fill: `var(--harness-${harness}-ink)` }}
      >
        {letters}
      </text>
    </symbol>
  );
}

export function LogoSprite() {
  return (
    <svg width="0" height="0" style="position:absolute" aria-hidden="true">
      <defs>
        {(Object.keys(harnesses) as HarnessName[]).map((h) => (
          <Symbol key={h} harness={h} />
        ))}
        <clipPath id="pennant" clipPathUnits="objectBoundingBox">
          <path d="M0 0H1L.77 .5L1 1H0Z" />
        </clipPath>
      </defs>
    </svg>
  );
}

// A pennant carrying the harness logo, clipped to the flag shape and outlined.
export function LogoFlag({
  harness,
  x,
  y,
  width,
  height,
  stroke,
}: {
  harness: HarnessName;
  x: number;
  y: number;
  width: number;
  height: number;
  stroke: number;
}) {
  const notch = x + width * 0.77;
  const d = `M${x} ${y}H${x + width}L${notch} ${y + height / 2}L${x + width} ${y + height}H${x}Z`;
  return (
    <>
      <g clip-path="url(#pennant)">
        <use href={`#${logoId(harness)}`} x={x} y={y} width={width} height={height} />
      </g>
      <path d={d} fill="none" stroke="#14304A" stroke-width={stroke} stroke-linejoin="round" />
    </>
  );
}
