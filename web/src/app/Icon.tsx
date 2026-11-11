import type { SurfaceId } from './surfaces';

const P: Record<string, preact.ComponentChildren> = {
  inbox: <path d="M3 13h5l1.5 3h5L16 13h5M5 5h14l2 8v6H3v-6z" />,
  ships: (
    <>
      <path d="M3 15h18l-2.4 4.2a2 2 0 0 1-1.7 1H7.1a2 2 0 0 1-1.7-1z" />
      <path d="M11 3v12" />
      <path d="M11 4.5 17.5 12H11z" />
    </>
  ),
  chat: <path d="M4 5h16v11H9l-5 4z" />,
  campaigns: <path d="M5 21V4M5 5h13l-3 4 3 4H5" />,
  routines: (
    <>
      <circle cx="12" cy="12" r="8.5" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  crew: (
    <>
      <rect x="5" y="7" width="14" height="12" rx="3" />
      <path d="M12 7V4M9.5 12.5h.01M14.5 12.5h.01" />
    </>
  ),
  preferences: <path d="M4 7h10M18 7h2M4 17h2M10 17h10M14 5v4M6 15v4" />,
  more: <path d="M5 12h.01M12 12h.01M19 12h.01" />,
  plus: <path d="M12 5v14M5 12h14" />,
  chevron: <path d="M7 10l5 5 5-5" />,
};

export function Icon({ name, size = 15 }: { name: SurfaceId | 'more' | 'chevron' | 'plus'; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      {P[name]}
    </svg>
  );
}
