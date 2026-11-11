import { useEffect } from 'preact/hooks';
import type { RefObject } from 'preact';

// Bounded by the screen, not the page, which grows at narrow widths; with the soft keyboard up the bottom edge follows
// the visual viewport so the prompt and key bar stay reachable.
export function useBridgePlacement(root: RefObject<HTMLDivElement | null>, present: boolean, full = false): void {
  useEffect(() => {
    const el = root.current;
    const main = el?.parentElement;
    if (!el || !main) return;
    const vv = window.visualViewport;
    const place = () => {
      const r = main.getBoundingClientRect();
      const covered = vv ? Math.max(0, window.innerHeight - vv.height - vv.offsetTop) : 0;
      el.style.left = full ? '0px' : `${Math.max(0, r.left)}px`;
      el.style.top = full ? '0px' : `${Math.max(0, r.top)}px`;
      // Beside the inbox drawer, which sits to the right of `main`, the bridge ends where `main` does.
      el.style.right = full ? '0px' : `${Math.max(0, window.innerWidth - r.right)}px`;
      el.style.bottom = `${covered > 40 ? covered : 0}px`;
    };
    place();
    document.documentElement.classList.add('bridge-open'); // the page behind doesn't scroll
    // The orders panel collapsing and a header banner coming or going move `main` without a window resize.
    const observer = new ResizeObserver(place);
    observer.observe(main);
    const header = document.querySelector('.app-header');
    if (header) observer.observe(header);
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place);
    vv?.addEventListener('resize', place);
    vv?.addEventListener('scroll', place);
    return () => {
      document.documentElement.classList.remove('bridge-open');
      observer.disconnect();
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place);
      vv?.removeEventListener('resize', place);
      vv?.removeEventListener('scroll', place);
    };
  }, [present, full]);
}
