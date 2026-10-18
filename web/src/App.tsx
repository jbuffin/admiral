import { useEffect } from 'preact/hooks';
import { config } from './config';
import { connect } from './connection';
import { Header } from './Header';
import { LogoSprite } from './Logos';
import { Marina, SetUpMarina } from './Marina';
import { Sidebar } from './Sidebar';
import { Lighthouse, Shore, Trees, WaterScenery } from './Scenery';

export function App() {
  useEffect(connect, []);
  const fleets = config.value?.fleets;
  return (
    <div class="app">
      <LogoSprite />
      <Header />
      <div class="harbor">
        <Sidebar />
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
