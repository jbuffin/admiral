// The Admiral mark, cleaned from designs/assets/admiral-mark.svg (metadata removed).
export function AdmiralMark({ size = 46 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 46 46" fill="none" aria-hidden="true">
      <path d="M5 20Q23 0 41 20Z" fill="#0B1F33" stroke="#FFC23D" stroke-width="2.5" stroke-linejoin="round" />
      <circle cx="23" cy="12" r="3" fill="#FFC23D" />
      <rect x="9" y="20" width="28" height="22" rx="4" fill="#FFC23D" stroke="#0B1F33" stroke-width="2.5" />
      <path fill="#14304A" d="M15 27h5v5h-5zM26 27h5v5h-5zM18 36h10v3H18z" />
    </svg>
  );
}
