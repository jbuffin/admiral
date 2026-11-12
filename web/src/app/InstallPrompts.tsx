import { useEffect, useState } from 'preact/hooks';
import { inStandalone } from './ios';
import { acceptInstallOffer, installedHere, installOffer, installState } from './install';
import { installSheetDue, markInstallSheetShown } from './installSheet';
import { Sheet } from './Sheet';

function HomeScreenSteps() {
  return (
    <>
      <ol class="install-steps">
        <li>Tap Share, then Add to Home Screen.</li>
        <li>Open Admiral from the Home Screen. The installed app keeps its own sign-in, so it asks for a new code: run <code>admiral pair</code> on your Mac.</li>
      </ol>
      <p class="install-why">The installed app is what push notifications need.</p>
    </>
  );
}

/** The Install app page in Preferences. */
export function InstallPage() {
  const state = installState({
    offered: installOffer.value !== undefined,
    installed: installedHere.value,
    standalone: inStandalone(),
    userAgent: navigator.userAgent,
    maxTouchPoints: navigator.maxTouchPoints,
  });
  if (state === 'installed') return <p>Admiral is installed on this device.</p>;
  if (state === 'ios') return <HomeScreenSteps />;
  if (state === 'unavailable') return <p>This browser isn't offering to install Admiral. It may already be installed here, or this browser may not install web apps (Chrome and Edge can).</p>;
  return (
    <>
      <p>Install Admiral as an app on this device, with its own window and push notifications.</p>
      <button type="button" class="btn primary" onClick={() => void acceptInstallOffer()}>
        Install
      </button>
    </>
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
      <HomeScreenSteps />
      <p class="install-why">You can find these steps again in Preferences → Install app.</p>
      <button type="button" class="btn primary" onClick={() => setOpen(false)}>
        Got it
      </button>
    </Sheet>
  );
}
