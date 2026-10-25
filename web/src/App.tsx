import { useEffect } from 'preact/hooks';
import { config } from './config';
import { connect } from './connection';
import { Bridge } from './Bridge';
import { SetupTerminal } from './SetupTerminal';
import { Header } from './Header';
import { InboxDrawer, InboxSheet } from './InboxPanel';
import { DockSheet } from './DockSheet';
import { Ghost, LaunchOrders } from './LaunchOrders';
import { LogoSprite } from './Logos';
import { Ships } from './OpenWater';
import { Marina, SetUpMarina } from './Marina';
import { PhoneShell } from './PhoneShell';
import { Sidebar } from './Sidebar';
import { phone } from './sizeClass';
import { Lighthouse, Shore, Trees, WaterScenery } from './Scenery';

export function App() {
  useEffect(connect, []);
  const fleets = config.value?.fleets;
  if (phone.value) {
    return (
      <div class="app">
        <LogoSprite />
        <Header />
        <PhoneShell>
          <Bridge />
          <SetupTerminal />
        </PhoneShell>
        <Ghost />
        <DockSheet />
        <LaunchOrders />
      </div>
    );
  }
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
            <div class="moorings">
              <div class="moorings-in">
                {fleets && (fleets.length === 0 ? <SetUpMarina /> : fleets.map((f) => <Marina key={f.key} fleet={f} />))}
              </div>
            </div>
          </div>
          <Ships />
          <Bridge />
          <SetupTerminal />
        </main>
        <InboxDrawer />
      </div>
      <InboxSheet />
      <Ghost />
      <LaunchOrders />
    </div>
  );
}
