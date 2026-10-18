import type { Dock, Fleet } from './config';
import { harnesses } from './harnesses';
import { armed, departed, dockKey, dragging, openLaunch, selected } from './launch';
import { LogoFlag } from './Logos';
import { Breakwater, Rowboat } from './Scenery';
import { Ship } from './Ship';

// Marina heights (tokens): 3 docks 300, otherwise the 2-dock height.
const heightFor = (docks: number) => (docks >= 3 ? 300 : 240);

// The top of each dock in a marina, from the prototype's fractions of the basin.
function dockTops(count: number, height: number): number[] {
  const fractions = count >= 3 ? [0.2, 0.5, 0.8] : count === 2 ? [0.28, 0.72] : [0.5];
  return fractions.map((f) => Math.round(46 + (height - 64) * f - 23) - 8);
}

// What replaces "All in port" on a dock whose config is wrong. The server's
// full text goes in the accessible name and title.
function marks(fleet: Fleet, dock: Dock): { short: string; full: string[] } | undefined {
  const full: string[] = [];
  let short = '';
  if (!dock.executable_found) {
    short = 'Not found';
    full.push(`Executable not found: ${dock.executable}`);
  }
  if (!fleet.can_launch) {
    short ||= 'No search roots';
    full.push('No search roots');
  }
  for (const w of dock.warnings) {
    short ||= 'Shared config dir';
    full.push(w);
  }
  return short ? { short, full } : undefined;
}

function DockView({ fleet, dock, top, index }: { fleet: Fleet; dock: Dock; top: number; index: number }) {
  const h = harnesses[dock.harness];
  const mark = marks(fleet, dock);
  const label = `${h.label} dock, ${fleet.name}`;
  const key = dockKey(fleet.key, dock.harness);
  const hot = dragging.value?.dock === key;
  const leaving = departed.value?.dock === key ? departed.value.n : 0;
  const status = armed.value ? (hot ? 'Let go to launch' : 'Drop here') : mark ? mark.short : 'All in port';
  return (
    <button
      type="button"
      class={armed.value ? (hot ? 'dock ready hot' : 'dock ready') : 'dock'}
      data-dock={key}
      onClick={() => {
        // A tap on a dock launches the selected card; with none selected it does nothing.
        if (selected.value) openLaunch(selected.value, fleet.key, dock.harness);
      }}
      style={`top:${top}px`}
      aria-label={mark ? `${label}. ${mark.full.join('. ')}` : label}
      title={mark ? mark.full.join('\n') : undefined}
    >
      {leaving > 0 && (
        <span class="moored leaving" key={`leaving-${leaving}`}>
          <Ship variant="moored" fleetColor={fleet.color} bot={h.bot} harness={dock.harness} />
        </span>
      )}
      <span class={leaving > 0 ? 'moored arriving' : 'moored'} key={`moored-${leaving}`}>
        <span class="bob" style={`display:block;animation-delay:-${(index * 0.9).toFixed(1)}s`}>
          <Ship variant="moored" fleetColor={fleet.color} bot={h.bot} harness={dock.harness} />
        </span>
      </span>
      <span class="pier">
        <svg width="34" height="24" viewBox="0 0 40 28" aria-hidden="true" style="flex:0 0 auto">
          <LogoFlag harness={dock.harness} kind="pier" />
        </svg>
        <span class="pier-text">
          <span class="pier-name">{h.label}</span>
          <span class={mark && !armed.value ? 'pier-status marked' : 'pier-status'}>{status}</span>
        </span>
        <span class="post" style="left:14px" />
        <span class="post" style="left:62px" />
      </span>
    </button>
  );
}

function Sign({ color, ink, children }: { color: string; ink: string; children: preact.ComponentChildren }) {
  return (
    <div class="sign" style={`background-color:${color};color:${ink}`}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={ink} stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="5" r="2" />
        <path d="M12 7v14M5 13a7 7 0 0 0 14 0M8 11h8" />
      </svg>
      {children}
    </div>
  );
}

function Basin({ height, rowboatTop, children }: { height: number; rowboatTop: number; children?: preact.ComponentChildren }) {
  return (
    <>
      <div class="quay" />
      <div class="basin">
        <svg viewBox="0 0 260 200" preserveAspectRatio="none" fill="none" aria-hidden="true">
          <g stroke="#7FE0E6" stroke-width="2" stroke-linecap="round" opacity=".7">
            <path d="M24 30q6-4 12 0t12 0" />
            <path d="M40 150q6-4 12 0t12 0" />
            <path d="M20 96q6-4 12 0t12 0" />
          </g>
        </svg>
      </div>
      <Breakwater height={height} />
      <Rowboat top={rowboatTop} />
      {children}
    </>
  );
}

export function Marina({ fleet }: { fleet: Fleet }) {
  const height = heightFor(fleet.docks.length);
  const tops = dockTops(fleet.docks.length, height);
  return (
    <section class="marina" aria-label={`${fleet.name} marina`} style={`height:${height}px`}>
      <Basin height={height} rowboatTop={fleet.docks.length >= 3 ? 58 : height - 52}>
        {fleet.docks.length === 0 && <p class="no-docks">No harnesses</p>}
        {fleet.docks.map((d, i) => (
          <DockView key={d.harness} fleet={fleet} dock={d} top={tops[i]} index={i} />
        ))}
      </Basin>
      <Sign color={fleet.color} ink={fleet.ink}>
        <span class="sign-name">{fleet.name}</span>
        <span class="sign-key">{fleet.key}</span>
      </Sign>
      {armed.value && <span class="drop-hint">{dragging.value ? 'Drop on a dock' : 'Tap a dock to launch'}</span>}
    </section>
  );
}

// With no fleets: one empty marina. Tapping it does nothing yet (u12).
export function SetUpMarina() {
  return (
    <section class="marina" aria-label="Set up a fleet" style={`height:${heightFor(0)}px`}>
      <Basin height={heightFor(0)} rowboatTop={heightFor(0) - 52} />
      <Sign color="#FFF6E3" ink="#14304A">
        <span class="sign-name">Set up a fleet</span>
      </Sign>
    </section>
  );
}
