import { useEffect, useState } from 'preact/hooks';
import { inStandalone } from './ios';
import { installSheetDue, markInstallSheetShown } from './installSheet';
import { Sheet } from './Sheet';

interface InstallPromptEvent extends Event {
  prompt(): Promise<unknown>;
  userChoice: Promise<unknown>;
}

/** Where the browser offers an install it fires beforeinstallprompt; the button keeps that event until the choice. */
export function InstallButton() {
  const [offer, setOffer] = useState<InstallPromptEvent>();
  useEffect(() => {
    const onOffer = (e: Event) => {
      e.preventDefault();
      setOffer(e as InstallPromptEvent);
    };
    const gone = () => setOffer(undefined);
    addEventListener('beforeinstallprompt', onOffer);
    addEventListener('appinstalled', gone);
    return () => {
      removeEventListener('beforeinstallprompt', onOffer);
      removeEventListener('appinstalled', gone);
    };
  }, []);
  if (!offer) return null;
  return (
    <button
      type="button"
      class="btn primary install-btn"
      onClick={async () => {
        await offer.prompt();
        await offer.userChoice.catch(() => {});
        setOffer(undefined);
      }}
    >
      Install
    </button>
  );
}

/** Shown once per device after it has paired in iOS Safari, where Add to Home Screen is the only way to install. */
export function InstallSheet() {
  const [open, setOpen] = useState(() => installSheetDue({ userAgent: navigator.userAgent, maxTouchPoints: navigator.maxTouchPoints, standalone: inStandalone() }));
  useEffect(() => {
    if (open) markInstallSheetShown();
  }, [open]);
  if (!open) return null;
  return (
    <Sheet label="Add Admiral to your Home Screen" onClose={() => setOpen(false)}>
      <ol class="install-steps">
        <li>Tap Share, then Add to Home Screen.</li>
        <li>Open Admiral from the Home Screen. The installed app keeps its own sign-in, so it asks for a new code: run <code>admiral pair</code> on your Mac.</li>
      </ol>
      <p class="install-why">The installed app is what push notifications need.</p>
      <button type="button" class="btn primary" onClick={() => setOpen(false)}>
        Got it
      </button>
    </Sheet>
  );
}
