import { isIOS } from './ios';

const KEY = 'admiral.installSheetShown';

const read = () => {
  try {
    return localStorage.getItem(KEY) !== null;
  } catch {
    return false;
  }
};

/** Once per device, on iOS Safari only: the installed app has nothing to install. */
export function installSheetDue({ userAgent, maxTouchPoints, standalone }: { userAgent: string; maxTouchPoints: number; standalone: boolean }): boolean {
  return isIOS({ userAgent, maxTouchPoints }) && !standalone && !read();
}

export function markInstallSheetShown() {
  try {
    localStorage.setItem(KEY, '1');
  } catch {
    /* blocked storage: the sheet may show again */
  }
}
