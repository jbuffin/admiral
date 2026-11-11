import { render } from 'preact';
import './fonts.css';
import './shell-tokens.css';
import './shell.css';
import { watchFleets } from './fleets';
import { startRouter } from './router';
import { Shell } from './Shell';
import { watchShips } from './shipsLink';

startRouter();
watchFleets();
watchShips();
render(<Shell />, document.getElementById('app')!);
