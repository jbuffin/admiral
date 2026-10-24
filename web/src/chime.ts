import { signal } from '@preact/signals';

// The needs-you chime: the prototype's three notes (index.html:853-872), WebAudio, no network.
// iPad Safari plays nothing until a gesture has started an AudioContext, so the first
// tap anywhere arms it, and turning sound on is a gesture too.

const KEY = 'admiral.muted';

function read(): boolean {
  try {
    return localStorage.getItem(KEY) === '1';
  } catch {
    return false;
  }
}

export const muted = signal(read());

let ctx: AudioContext | undefined;

function arm(): void {
  const C = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!C) return;
  ctx ??= new C();
  if (ctx.state === 'suspended') void ctx.resume();
}

for (const type of ['pointerup', 'click', 'keydown'] as const) {
  window.addEventListener(type, arm, { capture: true, passive: true });
}

const NOTES: [hz: number, at: number][] = [
  [660, 0],
  [880, 0.12],
  [1175, 0.24],
];

function play(): void {
  if (!ctx) return;
  const t = ctx.currentTime;
  for (const [hz, at] of NOTES) {
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    o.type = 'triangle';
    o.frequency.value = hz;
    g.gain.setValueAtTime(0.0001, t + at);
    g.gain.exponentialRampToValueAtTime(0.16, t + at + 0.02);
    g.gain.exponentialRampToValueAtTime(0.0001, t + at + 0.2);
    o.connect(g);
    g.connect(ctx.destination);
    o.start(t + at);
    o.stop(t + at + 0.22);
  }
}

// One chime, unless muted or no gesture has armed audio yet.
export function chime(): void {
  if (!muted.value) play();
}

export function toggleSound(): void {
  setMuted(!muted.value);
}

export function setMuted(next: boolean): void {
  muted.value = next;
  try {
    localStorage.setItem(KEY, muted.value ? '1' : '0');
  } catch {
    // The choice just isn't remembered.
  }
  arm();
  if (!muted.value) play();
}
