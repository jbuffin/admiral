import { useState } from 'preact/hooks';
import type { PRActor, PRDetail } from './api';
import { renderMarkdown } from './markdown';
import { checksLine, descriptionSource, firstLine, logHref, prTimeline, relativeTime, resolvedLabel, reviewerRows, reviewSummary, type TimelineItem } from './prSections';

const initials = (login: string) => login.replace(/[^a-z]/gi, '').slice(0, 2).toUpperCase();
const BotTag = ({ actor }: { actor: PRActor }) => (actor.bot ? <span class="bot-tag">bot</span> : null);
const who = (a: PRActor) => a.login || 'ghost';

function Checks({ detail }: { detail: PRDetail }) {
  const line = checksLine(detail);
  return (
    <section class="pr-section">
      <h4 class="pr-section-title">Checks</h4>
      <div class="checks-line">
        <span class={`dot ${line.tone === 'muted' ? '' : line.tone}`} />
        {line.text}
      </div>
      {detail.failing_checks.length ? (
        <ul class="check-list">
          {detail.failing_checks.map((f, i) => {
            const href = logHref(f.url);
            return (
              <li key={i}>
                <span class="pr-chip err">Failed</span>
                <span class="name">{f.name}</span>
                {href ? (
                  <a href={href} target="_blank" rel="noopener noreferrer">
                    Log
                  </a>
                ) : null}
              </li>
            );
          })}
        </ul>
      ) : null}
    </section>
  );
}

function Description({ body }: { body: string }) {
  const [open, setOpen] = useState(false);
  const source = descriptionSource(body);
  return (
    <section class="pr-section">
      <h4 class="pr-section-title">Description</h4>
      {source.trim() ? (
        <>
          <div class={`desc md${open ? '' : ' clamped'}`} dangerouslySetInnerHTML={{ __html: renderMarkdown(source) }} />
          <button type="button" class="link-btn" aria-expanded={open} onClick={() => setOpen(!open)}>
            {open ? 'Show less' : 'Show more'}
          </button>
        </>
      ) : (
        <p class="empty">No description.</p>
      )}
    </section>
  );
}

function Reviewers({ detail }: { detail: PRDetail }) {
  const [open, setOpen] = useState(false);
  const rows = reviewerRows(detail.reviews, detail.requested);
  return (
    <div class={`col col-side${open ? ' open' : ''}`}>
      <section class="pr-section">
        {rows.length ? (
          <>
            <button type="button" class="rev-summary" aria-expanded={open} onClick={() => setOpen(!open)}>
              {reviewSummary(rows)}
            </button>
            <h4 class="pr-section-title rev-title">Reviewers</h4>
            <ul class="reviewers">
              {rows.map((r) => (
                <li key={r.login} class="reviewer">
                  <span class={`avatar${r.bot ? ' bot' : ''}`}>{initials(r.login)}</span>
                  <span class="who">
                    {r.login} {r.bot ? <span class="bot-tag">bot</span> : null}
                  </span>
                  <span class={`pr-chip ${r.tone === 'muted' ? '' : r.tone}`}>{r.verdict}</span>
                </li>
              ))}
            </ul>
          </>
        ) : (
          <>
            <h4 class="pr-section-title">Reviewers</h4>
            <p class="empty">No reviewers yet.</p>
          </>
        )}
      </section>
    </div>
  );
}

const itemId = (i: TimelineItem) => i.url;

function Item({ item, now, folded, onToggle }: { item: TimelineItem; now: number; folded: boolean; onToggle: () => void }) {
  const cls = `item${item.author.bot ? ' bot' : ''}${folded ? ' folded' : ''}${item.resolved ? ' resolved' : ''}`;
  return (
    <div class={cls}>
      <div class="item-head">
        <span class="author">{who(item.author)}</span>
        <BotTag actor={item.author} />
        {item.kind === 'thread' ? <span class="file-ref">{item.ref}</span> : null}
        {item.resolved ? <span class="pr-chip">Resolved</span> : null}
        <span class="when">{relativeTime(item.at, now)}</span>
      </div>
      {item.body ? <p class="item-body">{folded ? firstLine(item.body) : item.body}</p> : null}
      {item.author.bot ? (
        <button type="button" class="link-btn" aria-expanded={!folded} onClick={onToggle}>
          {folded ? 'Show all' : 'Show less'}
        </button>
      ) : null}
      {item.replies.map((r, i) => (
        <div key={i} class="reply">
          <div class="item-head">
            <span class="author">{who(r.author)}</span>
            <BotTag actor={r.author} />
            <span class="when">{relativeTime(r.at, now)}</span>
          </div>
          <p class="item-body">{r.body}</p>
        </div>
      ))}
    </div>
  );
}

function Conversation({ detail }: { detail: PRDetail }) {
  const [resolvedOpen, setResolvedOpen] = useState(false);
  const [unfolded, setUnfolded] = useState<Set<string>>(new Set());
  const { open, resolved } = prTimeline(detail);
  const now = Date.now();
  const toggle = (id: string) =>
    setUnfolded((s) => {
      const next = new Set(s);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const show = (i: TimelineItem) => <Item key={itemId(i)} item={i} now={now} folded={i.author.bot && !unfolded.has(itemId(i))} onToggle={() => toggle(itemId(i))} />;
  return (
    <section class="pr-section">
      <h4 class="pr-section-title">Conversation</h4>
      <div class="timeline">
        {open.length + resolved.length === 0 ? <p class="empty">No reviews or comments yet.</p> : null}
        {open.map(show)}
        {resolved.length ? (
          <>
            <button type="button" class="resolved-fold" aria-expanded={resolvedOpen} onClick={() => setResolvedOpen(!resolvedOpen)}>
              {resolvedLabel(resolved.length)}
            </button>
            {resolvedOpen ? resolved.map(show) : null}
          </>
        ) : null}
      </div>
    </section>
  );
}

/** A PR's checks, description, reviewers and conversation, read-only; its fold state lives here, so a refresh of `detail` keeps it. */
export function PRSections({ detail }: { detail: PRDetail }) {
  return (
    <>
      <div class="pr-cols">
        <div class="col">
          <Checks detail={detail} />
          <Description body={detail.body} />
        </div>
        <Reviewers detail={detail} />
      </div>
      <Conversation detail={detail} />
    </>
  );
}
