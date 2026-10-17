import { useEffect } from 'preact/hooks';
import { config, connect } from './config';
import { Header } from './Header';
import { LogoSprite } from './Logos';
import { Marina, SetUpMarina } from './Marina';
import { Lighthouse, Shore, Trees, WaterScenery } from './Scenery';

export function App() {
  useEffect(connect, []);
  const fleets = config.value?.fleets;
  return (
    <div class="app">
      <LogoSprite />
      <Header />
      <div class="harbor">
        <aside class="orders" aria-label="Orders" />
        <main class="main" aria-label="Open water">
          <WaterScenery />
          <div class="hint">
            <span class="chip">Stripe = fleet · flag = harness · drag ships to arrange, tap to board</span>
          </div>
          <div class="sea" />
          <div class="land" aria-label="Marinas">
            <Shore />
            <Lighthouse />
            <Trees />
            {fleets && (fleets.length === 0 ? <SetUpMarina /> : fleets.map((f) => <Marina key={f.key} fleet={f} />))}
          </div>
        </main>
      </div>
    </div>
  );
}
