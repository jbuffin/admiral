import { render } from 'preact';
import './fonts.css';
import './shell-tokens.css';
import './shell.css';
import { watchFleets } from './fleets';
import { watchCampaigns } from './campaignsLink';
import { startRouter } from './router';
import { Shell } from './Shell';
import { watchShips } from './shipsLink';

startRouter();
watchFleets();
watchShips();
watchCampaigns();
render(<Shell />, document.getElementById('app')!);
