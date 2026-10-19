// Open water, land and the marina furniture, as Preact SVG built from the
// prototype (index.html:190-204, 277-297) and designs/assets.

export function WaterScenery() {
  return (
    <svg class="water-art" viewBox="0 0 1000 760" preserveAspectRatio="xMidYMid slice" fill="none" aria-hidden="true">
      <circle cx="420" cy="390" r="200" stroke="#5FCFD9" stroke-width="3" opacity="0.45" />
      <circle cx="420" cy="390" r="140" stroke="#5FCFD9" stroke-width="2" stroke-dasharray="4 10" opacity="0.45" />
      <path d="M420 170L440 370L640 390L440 410L420 610L400 410L200 390L400 370Z" fill="#3FC0CC" opacity="0.45" />
      <g class="drift" stroke="#9BEFF0" stroke-width="3" stroke-linecap="round" opacity="0.75">
        <path d="M60 120q12-9 24 0t24 0 24 0" />
        <path d="M300 60q12-9 24 0t24 0" />
        <path d="M560 250q12-9 24 0t24 0 24 0" />
        <path d="M150 330q12-9 24 0t24 0" />
        <path d="M90 600q12-9 24 0t24 0 24 0" />
        <path d="M420 700q12-9 24 0t24 0" />
        <path d="M600 560q12-9 24 0t24 0 24 0" />
        <path d="M480 140q12-9 24 0t24 0" />
      </g>
      <ellipse cx="40" cy="735" rx="70" ry="22" fill="#7C93A6" stroke="#14304A" stroke-width="3" />
    </svg>
  );
}

export function Shore() {
  const wave = 'Q4 40 14 80T14 160 14 240 14 320 14 400 14 480 14 560 14 640 14 720 14 800';
  return (
    <svg class="shore" viewBox="0 0 60 800" preserveAspectRatio="none" fill="none" aria-hidden="true">
      <path d="M0 0H22V800H0Z" fill="#29B2C2" />
      <path d={`M14 0${wave}L60 800V0Z`} fill="#F3D9A4" />
      <path
        d="M38 0Q46 40 38 80T38 160 38 240 38 320 38 400 38 480 38 560 38 640 38 720 38 800L60 800V0Z"
        fill="#8ACB68"
      />
      <path d={`M14 0${wave}`} stroke="#14304A" stroke-width="3" vector-effect="non-scaling-stroke" />
      <path
        d="M9 0Q-1 40 9 80T9 160 9 240 9 320 9 400 9 480 9 560 9 640 9 720 9 800"
        stroke="#DDF7F3"
        stroke-width="3"
        stroke-dasharray="10 8"
        vector-effect="non-scaling-stroke"
      />
    </svg>
  );
}

export function Lighthouse() {
  return (
    <svg class="lighthouse" width="26" height="48" viewBox="0 0 26 48" fill="none" aria-hidden="true">
      <path d="M8 44L10 14H16L18 44Z" fill="#FFFDF6" stroke="#14304A" stroke-width="2.2" stroke-linejoin="round" />
      <path d="M9.2 26H16.8L17.4 34H8.6Z" fill="#E5484D" />
      <rect x="9" y="7" width="8" height="7" fill="#FFC23D" stroke="#14304A" stroke-width="2" />
      <path d="M7 7L13 2L19 7Z" fill="#E5484D" stroke="#14304A" stroke-width="2" stroke-linejoin="round" />
      <path d="M5 44H21" stroke="#14304A" stroke-width="2.5" stroke-linecap="round" />
    </svg>
  );
}

const treeSpots = [
  'top:10px;right:20px',
  'top:18px;right:62px',
  'top:50%;right:24px',
  'bottom:6px;right:28px',
  'bottom:14px;right:84px',
  'bottom:4px;left:64px',
];

export function Trees() {
  return (
    <>
      {treeSpots.map((spot) => (
        <svg key={spot} class="tree" style={spot} width="34" height="40" viewBox="0 0 34 40" fill="none" aria-hidden="true">
          <path d="M17 26V38" stroke="#6B4A2E" stroke-width="4" stroke-linecap="round" />
          <circle cx="17" cy="16" r="13" fill="#4FA85A" stroke="#14304A" stroke-width="2.5" />
          <circle cx="12.5" cy="12" r="4" fill="#7CC77A" />
        </svg>
      ))}
    </>
  );
}

export function Rowboat({ top }: { top: number }) {
  return (
    <svg class="rowboat" style={`left:26px;top:${top}px`} width="34" height="18" viewBox="0 0 34 18" fill="none" aria-hidden="true">
      <path d="M2 6H32L28 13Q17 17 6 13Z" fill="#C98A55" stroke="#14304A" stroke-width="2" stroke-linejoin="round" />
      <path d="M10 3L22 15" stroke="#6B4A2E" stroke-width="2" stroke-linecap="round" />
    </svg>
  );
}

const shapes = [
  '46% 54% 42% 58% / 52% 44% 56% 48%',
  '58% 42% 50% 50% / 44% 56% 44% 56%',
  '50% 50% 60% 40% / 56% 48% 52% 44%',
  '40% 60% 52% 48% / 48% 52% 40% 60%',
];
const fills = ['#8E9AA6', '#A7B2BC', '#7C8996', '#9AA5AF'];
const jitter = [[0, 0], [-4, 3], [3, -3], [-2, -4], [4, 2], [-3, 4], [2, -2]];
const steps = [0, 0.17, 0.34, 0.5, 0.66, 0.8, 0.93];
type Point = [number, number];

interface Rock {
  x: number;
  y: number;
  size: number;
  k: number;
}

// Rocks along a quadratic curve, the arms of a marina's breakwater.
function arm(p0: Point, c: Point, p2: Point, seed: number): Rock[] {
  const out: Rock[] = [];
  steps.forEach((t, i) => {
    const k = i + seed;
    const x = (1 - t) ** 2 * p0[0] + 2 * (1 - t) * t * c[0] + t * t * p2[0];
    const y = (1 - t) ** 2 * p0[1] + 2 * (1 - t) * t * c[1] + t * t * p2[1];
    const size = Math.round(36 - 18 * t + (k % 3) * 4);
    out.push({ x: x + jitter[k % 7][0], y: y + jitter[k % 7][1], size, k });
    if (i === 1 || i === 3 || i === 5) {
      const small = Math.round(size * 0.55);
      out.push({ x: x - size * 0.45, y: y + (i % 2 ? 1 : -1) * size * 0.35, size: small, k: k + 2 });
    }
  });
  return out;
}

// The breakwater of a marina `height` px tall.
export function Breakwater({ height }: { height: number }) {
  const reach = Math.round(Math.max(40, (height - 40 - 110) / 2));
  const rocks = arm([24, 40], [-48, 40], [-48, 40 + reach], 0).concat(
    arm([24, height - 12], [-48, height - 12], [-48, height - 12 - reach], 5),
  );
  return (
    <>
      {rocks.map((r) => (
        <span
          key={`${r.x}:${r.y}`}
          class="rock"
          aria-hidden="true"
          style={`left:${(r.x - r.size / 2).toFixed(1)}px;top:${(r.y - r.size * 0.42).toFixed(1)}px;width:${r.size}px;height:${Math.round(r.size * 0.84)}px;border-radius:${shapes[r.k % 4]};background-color:${fills[r.k % 4]};transform:rotate(${((r.k * 41) % 70) - 35}deg)`}
        />
      ))}
    </>
  );
}
