import { useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { config } from './config';
import { connect } from './connection';
import { LogoSprite } from './Logos';
import { Ships } from './OpenWater';
import { Marina, SetUpMarina } from './Marina';
import { Lighthouse, Shore, Trees, WaterScenery } from './Scenery';

// A narrower window sees the same scene as a wide one, scaled down to fit.
const STAGE_MIN_WIDTH = 900;

const stageScale = () => Math.min(1, window.innerWidth / STAGE_MIN_WIDTH);

// The marinas shrink together, so every dock shows without a scroll.
function useFit() {
  const moorings = useRef<HTMLDivElement>(null);
  const inner = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const box = moorings.current;
    const content = inner.current;
    if (!box || !content) return;
    const fit = () => {
      const k = Math.min(1, box.clientHeight / Math.max(1, content.offsetHeight));
      box.style.setProperty('--fit', k > 0 ? String(k) : '1');
    };
    fit();
    if (typeof ResizeObserver === 'undefined') return;
    const watch = new ResizeObserver(fit);
    watch.observe(box);
    watch.observe(content);
    return () => watch.disconnect();
  }, []);
  return { moorings, inner };
}

function useStage() {
  const stage = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const apply = () => stage.current?.style.setProperty('--stage', String(stageScale()));
    apply();
    window.addEventListener('resize', apply);
    return () => window.removeEventListener('resize', apply);
  }, []);
  return stage;
}

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
