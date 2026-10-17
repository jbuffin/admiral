import type { HarnessName } from './config';
import { LogoFlag } from './Logos';

export interface ShipProps {
  fleetColor: string;
  bot: string;
  harness: HarnessName;
  variant?: 'moored' | 'sea';
  // Later units fill this; a ship that needs you shakes and its flag flutters faster.
  state?: 'underway' | 'needs-you' | 'anchored';
}

const OUTLINE = '#14304A';
const SAIL = '#FFF6E3';

// Ships face left. Cleaned from designs/assets/ship.svg: no metadata, the
// fleet color, the bot's color and the harness's logo are props.
export function Ship({ fleetColor, bot, harness, variant = 'sea', state = 'underway' }: ShipProps) {
  if (variant === 'moored') return <MooredShip fleetColor={fleetColor} bot={bot} harness={harness} />;
  const attention = state === 'needs-you';
  return (
    <span class={attention ? 'attn' : 'bob'} style="display:block">
      <svg width="100" height="96" viewBox="0 0 100 96" fill="none" aria-hidden="true">
        <path d="M4 90q8-5 16 0t16 0 16 0 16 0 16 0 16 0" stroke="#DDF7F3" stroke-width="3" stroke-linecap="round" />
        <path d="M14 62L2 52" stroke={OUTLINE} stroke-width="3" stroke-linecap="round" />
        <path d="M50 64V3" stroke={OUTLINE} stroke-width="4" stroke-linecap="round" />
        <path d="M36 14h28q4 9 0 18H36q4-9 0-18Z" fill={SAIL} stroke={OUTLINE} stroke-width="2.5" stroke-linejoin="round" />
        <path d="M31 36h38q6 11 0 22H31q5-11 0-22Z" fill={SAIL} stroke={OUTLINE} stroke-width="2.5" stroke-linejoin="round" />
        <g class={attention ? 'flag frantic' : 'flag wave'}>
          <LogoFlag harness={harness} x={51} y={2} width={29} height={15} stroke={2} />
        </g>
        <path d="M72 64V52h24v12Z" fill="#8E5331" stroke={OUTLINE} stroke-width="2.5" stroke-linejoin="round" />
        <Captain bot={bot} stroke={2.4} />
        <path d="M72 34Q84 21 96 34Z" fill={OUTLINE} />
        <circle cx="84" cy="29.5" r="1.8" fill="#FFC23D" />
        <path d="M4 62H98L93 76Q86 86 70 86H28Q12 84 4 62Z" fill="#A0613A" stroke={OUTLINE} stroke-width="3" stroke-linejoin="round" />
        <path d="M6.5 67H96.2L94.4 73H9Z" fill={fleetColor} stroke={OUTLINE} stroke-width="1.5" stroke-linejoin="round" />
        {[38, 54, 70].map((cx) => (
          <circle key={cx} cx={cx} cy="79.5" r="2.6" fill={SAIL} stroke={OUTLINE} stroke-width="1.8" />
        ))}
      </svg>
    </span>
  );
}

function Captain({ bot, stroke }: { bot: string; stroke: number }) {
  return (
    <>
      <rect x="75" y="33" width="18" height="18" rx="3" fill={bot} stroke={OUTLINE} stroke-width={stroke} />
      <rect x="78.5" y="39" width="3.6" height="3.6" fill={OUTLINE} />
      <rect x="85.5" y="39" width="3.6" height="3.6" fill={OUTLINE} />
    </>
  );
}

// Tied up at a dock: thicker strokes, no portholes, a plain flag in the
// harness color with no logo, and the flag stays still.
function MooredShip({ fleetColor, bot, harness }: Pick<ShipProps, 'fleetColor' | 'bot' | 'harness'>) {
  return (
    <svg width="56" height="54" viewBox="0 0 100 96" fill="none" aria-hidden="true">
      <path d="M50 64V3" stroke={OUTLINE} stroke-width="5" stroke-linecap="round" />
      <path
        d="M36 14h28q4 9 0 18H36q4-9 0-18ZM31 36h38q6 11 0 22H31q5-11 0-22Z"
        fill={SAIL}
        stroke={OUTLINE}
        stroke-width="3.5"
        stroke-linejoin="round"
      />
      <path
        d="M51 2H80L73 9.5 80 17H51Z"
        style={{ fill: `var(--harness-${harness}-flag)` }}
        stroke={OUTLINE}
        stroke-width="3"
        stroke-linejoin="round"
      />
      <Captain bot={bot} stroke={3.2} />
      <path d="M14 62L2 52" stroke={OUTLINE} stroke-width="4" stroke-linecap="round" />
      <path d="M4 62H98L93 76Q86 86 70 86H28Q12 84 4 62Z" fill="#A0613A" stroke={OUTLINE} stroke-width="4" stroke-linejoin="round" />
      <path d="M7 67H96L94 73.5H9.5Z" fill={fleetColor} />
      <path d="M6 92q8-5 16 0t16 0 16 0 16 0 16 0" stroke="#DDF7F3" stroke-width="4" stroke-linecap="round" />
    </svg>
  );
}
