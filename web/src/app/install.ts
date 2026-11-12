import { signal } from '@preact/signals';
import { isIOS } from './ios';

export interface InstallPromptEvent extends Event {
  prompt(): Promise<unknown>;
  userChoice: Promise<{ outcome?: string } | undefined>;
}

export type InstallState = 'offer' | 'installed' | 'ios' | 'unavailable';

/** What the Install app page shows on this device. */
export function installState({ offered, installed, standalone, userAgent, maxTouchPoints }: { offered: boolean; installed: boolean; standalone: boolean; userAgent: string; maxTouchPoints: number }): InstallState {
  if (standalone || installed) return 'installed';
  if (offered) return 'offer';
  if (isIOS({ userAgent, maxTouchPoints })) return 'ios';
  return 'unavailable';
}

export const installOffer = signal<InstallPromptEvent | undefined>(undefined);
export const installedHere = signal(false);

/** The browser fires beforeinstallprompt once, at load, so the offer is kept from app start for the Preferences page to use later. */
export function listenForInstall() {
  addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault();
    installOffer.value = e as InstallPromptEvent;
  });
  addEventListener('appinstalled', () => {
    installOffer.value = undefined;
    installedHere.value = true;
  });
}

export async function acceptInstallOffer() {
  const offer = installOffer.value;
  if (!offer) return;
  try {
    await offer.prompt();
    const choice = await offer.userChoice;
    if (choice?.outcome === 'accepted') installedHere.value = true;
  } catch {
    /* the prompt was refused: the offer is spent either way */
  } finally {
    installOffer.value = undefined;
  }
}
