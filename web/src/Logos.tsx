import { harnesses, logoId } from './harnesses';
import { parseLogoSvg } from './logoSvg';
import type { HarnessName } from './config';

// Official logo files, when the engineer has put them in web/logos/official/
// (gitignored; see docs/personal-builds.md). Each is named for its harness and carries its
// own field, because the vendors' usage rules set the field.
const official = import.meta.glob('/logos/official/*.svg', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>;

const officialSymbol = (h: HarnessName) => parseLogoSvg(official[`/logos/official/${h}.svg`]);
const isOfficial = (h: HarnessName) => officialSymbol(h) !== undefined;

// The flag draws a logo by id, so swapping logos changes only these symbols.
// The placeholders are the lettered set and carry their own field. An official
// symbol is only the mark; the flag draws its field behind it.
function Symbol({ harness }: { harness: HarnessName }) {
  const o = officialSymbol(harness);
  if (o) {
    return (
      <symbol id={logoId(harness)} viewBox={o.viewBox} preserveAspectRatio="xMidYMid meet" dangerouslySetInnerHTML={{ __html: o.inner }} />
    );
  }
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

// The two flag sizes: the pier's flag in its own 40×28 box, and the flag at a
// ship's masthead in the ship's 100×96 box.
const flags = {
  pier: { x: 2, y: 3, width: 36, height: 22, stroke: 2.6 },
  sea: { x: 51, y: 2, width: 29, height: 15, stroke: 2 },
  bridge: { x: 2, y: 2, width: 36, height: 22, stroke: 2.5 },
};
type FlagKind = keyof typeof flags;

// The swallow-tail pennant, with its notch 77% of the way across.
const pennant = ({ x, y, width, height }: (typeof flags)[FlagKind]) =>
  `M${x} ${y}H${x + width}L${x + width * 0.77} ${y + height / 2}L${x + width} ${y + height}H${x}Z`;

// A harness this build doesn't know: a plain "?" flag.
const UNKNOWN = 'logo-unknown';

export function LogoSprite() {
  return (
    <svg width="0" height="0" style="position:absolute" aria-hidden="true">
      <defs>
        <symbol id={UNKNOWN} viewBox="0 0 40 28" preserveAspectRatio="xMidYMid slice">
          <rect width="40" height="28" fill="#E9E2D0" />
          <text x="20" y="19" text-anchor="middle" font-family="Bricolage Grotesque, sans-serif" font-weight="800" font-size="13" fill="#14304A">
            ?
          </text>
        </symbol>
        {(Object.keys(harnesses) as HarnessName[]).map((h) => (
          <Symbol key={h} harness={h} />
        ))}
        {(Object.keys(flags) as FlagKind[]).map((k) => (
          <clipPath key={k} id={`pennant-${k}`}>
            <path d={pennant(flags[k])} />
          </clipPath>
        ))}
      </defs>
    </svg>
  );
}

// A pennant carrying the harness logo, clipped to the flag shape and outlined.
// Behind an official logo it draws the harness's field over the whole pennant
// and sets the mark whole, centered in the solid part with a little clear space.
export function LogoFlag({ harness, kind }: { harness: string; kind: FlagKind }) {
  const f = flags[kind];
  const known = Object.hasOwn(harnesses, harness) ? (harness as HarnessName) : undefined;
  const pad = f.height * 0.15;
  const mark = { x: f.x + pad, y: f.y + pad, width: f.width * 0.77 - 2 * pad, height: f.height - 2 * pad };
  return (
    <>
      <g clip-path={`url(#pennant-${kind})`}>
        {known && isOfficial(known) ? (
          <>
            <rect x={f.x} y={f.y} width={f.width} height={f.height} fill={harnesses[known].field} />
            <use href={`#${logoId(known)}`} {...mark} />
          </>
        ) : (
          <use href={`#${known ? logoId(known) : UNKNOWN}`} x={f.x} y={f.y} width={f.width} height={f.height} />
        )}
      </g>
      <path d={pennant(f)} fill="none" stroke="#14304A" stroke-width={f.stroke} stroke-linejoin="round" />
    </>
  );
}
