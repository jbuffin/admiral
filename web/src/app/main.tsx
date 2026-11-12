import { render } from 'preact';
import './fonts.css';
import './shell-tokens.css';
import './shell.css';
import { watchFleets } from './fleets';
import { watchCampaigns } from './campaignsLink';
import { watchHoldFire } from './holdFireLink';
import { listenForInstall } from './install';
import { startRouter } from './router';
import { Shell } from './Shell';
import { checkBuild } from './buildCheck';
import { onShipsOpen, watchShips } from './shipsLink';

listenForInstall();
startRouter();
watchFleets();
watchShips();
watchCampaigns();
watchHoldFire();
render(<Shell />, document.getElementById('app')!);

// Each socket open asks which build the server runs, so a reconnect to an updated server reloads the shell too.
onShipsOpen(() => void checkBuild());

// Plain-HTTP LAN addresses have no service worker; the shell works without one. The dev server serves no built worker.
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js', { scope: '/app/' }).catch(() => {});
}
