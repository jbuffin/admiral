import { LogoFlag } from './Logos';
import type { WireCrew } from './ships';

const SIZE = { sea: [38, 33], flotilla: [52, 46] } as const; // tokens.json size.crewBoat, crewBoatFlotilla

// A teammate's crew boat (designs/assets/crew-boat.svg, anchored: crew-boat-anchored.svg), on the sea and in the
// flotilla. The flag carries the boat's own harness logo, where the asset has a plain pennant.
export function CrewBoat({ boat, stripe, bot, size }: { boat: WireCrew; stripe: string; bot: string; size: keyof typeof SIZE }) {
  const anchored = boat.status === 'anchored';
  const [width, height] = SIZE[size];
  return (
    <svg width={width} height={height} viewBox="0 0 64 56" fill="none" aria-hidden="true">
      <path d="M4 50 q7 -5 14 0 t14 0 t14 0 t14 0" stroke="#DDF7F3" stroke-width="2.5" stroke-linecap="round" />
      <path d="M4 37 L60 37 L54 46 Q46 51 32 51 L18 51 Q8 49 4 37 Z" fill="#A0613A" stroke="#14304A" stroke-width="2.5" stroke-linejoin="round" />
      <path d="M6 40 L59 40 L57.5 44.5 L8 44.5 Z" fill={anchored ? '#9AA5AF' : stripe} />
      <path d="M30 37 L30 6" stroke="#14304A" stroke-width="3" stroke-linecap="round" />
      {anchored ? (
        <>
          <path d="M32 27 Q42 29 46 33 L32 33 Z" fill="#E4D6B8" stroke="#14304A" stroke-width="2" stroke-linejoin="round" />
          <path d="M22 31 L48 31" stroke="#14304A" stroke-width="2.4" stroke-linecap="round" />
        </>
      ) : (
        <>
          <path d="M32 9 L32 33 L52 33 Z" fill="#FFF6E3" stroke="#14304A" stroke-width="2.2" stroke-linejoin="round" />
          <path d="M28 15 L28 33 L19 33 Z" fill="#FFF6E3" stroke="#14304A" stroke-width="2" stroke-linejoin="round" />
        </>
      )}
      <g class={anchored ? 'flag' : 'flag wave'}>
        <LogoFlag harness={boat.harness} kind="crew" />
      </g>
      <rect x="4" y="24" width="13" height="13" rx="2" fill={bot} stroke="#14304A" stroke-width="2" />
      <rect x="7" y="28" width="2.5" height="2.5" fill="#14304A" />
      <rect x="11.5" y="28" width="2.5" height="2.5" fill="#14304A" />
      {anchored && (
        <>
          <circle cx="8" cy="8" r="7" fill="#C2412A" stroke="#14304A" stroke-width="2" />
          <path d="M5.5 5.5l5 5M10.5 5.5l-5 5" stroke="#FFFDF6" stroke-width="2.2" stroke-linecap="round" />
        </>
      )}
    </svg>
  );
}
