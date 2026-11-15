import { Button } from './Button';
import { retryShips } from './shipsLink';

export function Unreachable() {
  return (
    <div class="unreachable" role="alert">
      <div class="unreachable-card">
        <h1>Can't reach your Mac</h1>
        <ul>
          <li>Tailscale on here?</li>
          <li>Mac awake?</li>
          <li>Admiral running?</li>
        </ul>
        <Button variant="primary" label="Retry" onClick={retryShips} />
      </div>
    </div>
  );
}
