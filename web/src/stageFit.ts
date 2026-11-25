import { useLayoutEffect, useRef } from 'preact/hooks';
import { useSubscribe } from './app/hooks/subscribe';

// A narrower window sees the same scene as a wide one, scaled down to fit.
const STAGE_MIN_WIDTH = 900;

const stageScale = () => Math.min(1, window.innerWidth / STAGE_MIN_WIDTH);

// The marinas shrink together, so every dock shows without a scroll.
export function useFit() {
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

export function useStage() {
  const stage = useRef<HTMLDivElement>(null);
  const apply = () => stage.current?.style.setProperty('--stage', String(stageScale()));
  useLayoutEffect(apply, []);
  useSubscribe(window, 'resize', apply);
  return stage;
}
