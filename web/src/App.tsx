import { useEffect } from 'preact/hooks';
import { config } from './config';
import { connect } from './connection';
import { LogoSprite } from './Logos';
import { Ships } from './OpenWater';
import { Marina, SetUpMarina } from './Marina';
import { useFit, useStage } from './stageFit';
import { Lighthouse, Shore, Trees, WaterScenery } from './Scenery';

export function App() {
  useEffect(connect, []);
  const fleets = config.value?.fleets;
  const stage = useStage();
  const { moorings, inner } = useFit();
  return (
    <div class="app">
      <LogoSprite />
      <div class="stage" ref={stage}>
        <main class="main" aria-label="Open water">
          <WaterScenery />
          <div class="sea" />
          <div class="land" aria-label="Marinas">
            <Shore />
            <Lighthouse />
            <Trees />
            <div class="moorings" ref={moorings}>
              <div class="moorings-in" ref={inner}>
                {fleets && (fleets.length === 0 ? <SetUpMarina /> : fleets.map((f) => <Marina key={f.key} fleet={f} />))}
              </div>
            </div>
          </div>
          <Ships />
        </main>
      </div>
    </div>
  );
}
