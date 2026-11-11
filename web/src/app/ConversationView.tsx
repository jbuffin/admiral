import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { call as post, shipUrl, type Item } from './api';
import type { AnswerCard } from './answerCard';
import { Icon } from './Icon';
import type { Conversation } from './conversation';
import { renderMarkdown } from './markdown';
import { blocksOf, capOutput, toolRowLabel, workingLabel } from './turns';

function Output({ text, error }: { text: string; error?: boolean }) {
  const [all, setAll] = useState(false);
  const shown = capOutput(text, all);
  return (
    <div class={`tool-output${error ? ' error' : ''}`}>
      <pre>{shown.text}</pre>
      {shown.total !== undefined ? (
        <button type="button" class="btn" onClick={() => setAll(true)}>
          Show all {shown.total} lines
        </button>
      ) : null}
    </div>
  );
}

function ToolCall({ item, open: initial }: { item: Item; open: boolean }) {
  const [open, setOpen] = useState(initial);
  const tool = item.tool;
  if (!tool) return null;
  return (
    <div class="tool-call">
      <button type="button" class="tool-head" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span class={`tool-dot${tool.done ? '' : ' running'}${tool.error ? ' error' : ''}`} aria-hidden="true" />
        <span class="tool-name">{tool.name}</span>
        {tool.summary ? <span class="tool-summary">{tool.summary}</span> : null}
      </button>
      {open ? tool.output ? <Output text={tool.output} error={tool.error} /> : <p class="muted tool-empty">{tool.done ? 'No output' : 'Running…'}</p> : null}
    </div>
  );
}

function FoldedTools({ calls, durationMs }: { calls: Item[]; durationMs: number }) {
  const [open, setOpen] = useState(false);
  return (
    <div class="tools-folded">
      <button type="button" class="fold-head" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span class={`fold-chevron${open ? ' open' : ''}`}>
          <Icon name="chevron" size={14} />
        </span>
        {toolRowLabel(durationMs, calls.map((c) => c.tool?.name ?? ''))}
      </button>
      {open ? calls.map((c) => <ToolCall key={c.id} item={c} open />) : null}
    </div>
  );
}

function Message({ item }: { item: Item }) {
  if (item.kind === 'user') return <div class="msg user">{item.text}</div>;
  if (item.kind === 'assistant') return <div class="msg assistant md" dangerouslySetInnerHTML={{ __html: renderMarkdown(item.text ?? '') }} />;
  if (item.kind === 'compaction')
    return (
      <div class="compaction" role="separator">
        <span>Conversation compacted</span>
      </div>
    );
  if (item.kind === 'question') return <div class="msg note">{item.question?.text ?? item.text}</div>;
  if (item.kind === 'notice') return item.meta ? null : <div class="msg note">{item.text}</div>;
  return null;
}

function useTick(ms: number) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

function Card({ title, children }: { title: string; children?: preact.ComponentChildren }) {
  return (
    <div class="convo-card">
      <p class="convo-card-title">{title}</p>
      {children}
    </div>
  );
}

const cardKey = (c: AnswerCard) => (c.kind === 'permission' ? `p:${c.reason}:${c.call?.id ?? ''}` : c.kind === 'question' ? `q:${c.reason}:${c.item}` : c.kind);

function AnswerCardView({ card, session, crew, openTerminal }: { card: AnswerCard; session: string; crew?: string; openTerminal: () => void }) {
  const [busy, setBusy] = useState(false);
  const [gone, setGone] = useState<string | null>(null);
  // Set before the first re-render, so a double tap sends once.
  const sending = useRef(false);
  const key = cardKey(card);
  // A refused card must not hide the next prompt, which can share its key.
  useEffect(() => {
    sending.current = false;
    setBusy(false);
    setGone(null);
  }, [key]);
  if (card.kind === 'none' || gone === key) return null;
  if (card.kind === 'terminal')
    return (
      <Card title="Answer this in the terminal">
        <button type="button" class="btn" onClick={openTerminal}>
          Open terminal
        </button>
      </Card>
    );
  const send = async (body: object) => {
    if (sending.current) return;
    sending.current = true;
    setBusy(true);
    const res = await post('POST', `${shipUrl(session, crew)}/answer`, body);
    if (!res.ok) setGone(key);
  };
  if (card.kind === 'permission') {
    const tool = card.call?.tool;
    const input = tool?.input;
    return (
      <Card title={`${card.tool} needs your permission`}>
        {input ? <pre class="approve-input">{input}</pre> : null}
        {tool && (input ? tool.summary && tool.summary !== input : true) ? <p class="muted approve-note">{tool.summary ?? tool.name}</p> : null}
        <div class="card-actions">
          <button type="button" class="btn" disabled={busy} onClick={() => void send({ card: 'permission', reason: card.reason, choice: 'allow' })}>
            Allow once
          </button>
          <button type="button" class="btn" disabled={busy} onClick={() => void send({ card: 'permission', reason: card.reason, choice: 'deny' })}>
            Deny
          </button>
        </div>
      </Card>
    );
  }
  return (
    <Card title={card.text}>
      <div class="card-actions">
        {card.options.map((label, i) => (
          <button key={i} type="button" class="btn card-option" disabled={busy} onClick={() => void send({ card: 'question', reason: card.reason, item: card.item, option: i + 1 })}>
            <span>{label}</span>
            {card.descriptions[i] ? <span class="muted card-option-note">{card.descriptions[i]}</span> : null}
          </button>
        ))}
      </div>
    </Card>
  );
}

function ConversationBody({ conv, harness, anchored = false, working, noneTitle, openTerminal, loadOlder }: { conv: Conversation; harness: string; anchored?: boolean; working: boolean; noneTitle?: string; openTerminal: () => void; loadOlder: () => void }) {
  const now = useTick(15_000);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const count = conv.items.length;

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [count, working, conv.state]);

  if (conv.state === 'loading') return <p class="line">Loading…</p>;
  const terminal = (
    <button type="button" class="btn" onClick={openTerminal}>
      Open terminal
    </button>
  );
  if (conv.state === 'none')
    return noneTitle ? (
      <Card title={noneTitle}>{terminal}</Card>
    ) : harness !== 'claude' && harness !== 'copilot' ? (
      <Card title="No conversation for this harness">{terminal}</Card>
    ) : anchored ? (
      <Card title="No conversation recorded">{terminal}</Card>
    ) : (
      <Card title="Starting…">{terminal}</Card>
    );
  if (conv.state === 'unreadable') return <Card title="Admiral can't read this conversation">{terminal}</Card>;
  if (conv.state === 'gone') return <Card title="Conversation no longer available" />;

  const blocks = blocksOf(conv.items);
  return (
    <div
      class="convo"
      ref={scroller}
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}
    >
      {conv.more ? (
        <button type="button" class="btn convo-older" onClick={loadOlder}>
          Load earlier messages
        </button>
      ) : null}
      {blocks.length === 0 && !working ? <p class="line">No messages yet.</p> : null}
      {blocks.map((b, i) =>
        b.kind === 'item' ? (
          <Message key={b.item.id} item={b.item} />
        ) : b.durationMs !== undefined ? (
          <FoldedTools key={b.calls[0].id} calls={b.calls} durationMs={b.durationMs} />
        ) : (
          <div key={b.calls[0].id ?? i} class="tools-running">
            {b.calls.map((c) => (
              <ToolCall key={c.id} item={c} open={false} />
            ))}
          </div>
        ),
      )}
      {working ? <p class="working">{workingLabel(conv.items, now)}</p> : null}
    </div>
  );
}

/** The conversation with the card that answers its prompt in place, always in the same position below it. */
export function ConversationView(props: { conv: Conversation; harness: string; anchored?: boolean; working: boolean; noneTitle?: string; card: AnswerCard; session: string; crew?: string; openTerminal: () => void; loadOlder: () => void }) {
  const { card, session, crew, openTerminal } = props;
  // A state that has its own Open terminal card needs no second one; a permission card needs no transcript.
  const shown: AnswerCard = card.kind === 'terminal' && props.conv.state !== 'ok' ? { kind: 'none' } : card;
  return (
    <>
      <ConversationBody conv={props.conv} harness={props.harness} anchored={props.anchored} working={props.working} noneTitle={props.noneTitle} openTerminal={openTerminal} loadOlder={props.loadOlder} />
      <AnswerCardView card={shown} session={session} crew={crew} openTerminal={openTerminal} />
    </>
  );
}
