/** iPadOS Safari reports a Mac user agent, so a touch screen on a Mac user agent is an iPad. */
export function isIOS({ userAgent, maxTouchPoints }: { userAgent: string; maxTouchPoints: number }): boolean {
  return /iPhone|iPad|iPod/.test(userAgent) || (/Macintosh/.test(userAgent) && maxTouchPoints > 1);
}

export function isStandalone({ navigatorStandalone, displayModeStandalone }: { navigatorStandalone?: boolean; displayModeStandalone: boolean }): boolean {
  return navigatorStandalone === true || displayModeStandalone;
}

export const inStandalone = () =>
  isStandalone({
    navigatorStandalone: (navigator as Navigator & { standalone?: boolean }).standalone,
    displayModeStandalone: matchMedia('(display-mode: standalone)').matches,
  });
