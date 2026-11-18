import type { ComponentChildren } from 'preact';

/** A titled group of rows with its count, as Orders and a module's page draw one. */
export function ListGroup({ id, title, count, muted, children }: { id: string; title: string; count: number; muted: boolean; children: ComponentChildren }) {
  return (
    <section class="ogroup" aria-labelledby={id}>
      <h2 id={id} class="ogroup-head">
        {muted ? null : <span class="dot needs" aria-hidden="true" />}
        {title} <span class="count">{count}</span>
      </h2>
      <ul class="ship-list">{children}</ul>
    </section>
  );
}
