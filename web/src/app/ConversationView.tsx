import { useEffect, useLayoutEffect, useRef, useState } from 'preact/hooks';
import { call as post, shipUrl, SLOW_TIMEOUT_MS, type Item } from './api';
import type { AnswerCard } from './answerCard';
import { hasConversationAdapter } from './conversationGate';
import { Actions, Button } from './Button';
import { Icon } from './Icon';
import type { Conversation } from './conversation';
import { renderMarkdown } from './markdown';
import { QuestionCall } from './QuestionCall';
import { when } from './when';
import { blocksOf, capOutput, roundLabel, roundNote, toolRowLabel, workingLabel } from './turns';

function Output({ text, error }: { text: string; error?: boolean }) {
  const [all, setAll] = useState(false);
  const shown = capOutput(text, all);
  return (
    <div class={`tool-output${error ? ' error' : ''}`}>
      <pre>{shown.text}</pre>
      {shown.total !== undefined ? (
        <Button label={`Show all ${shown.total} lines`} onClick={() => setAll(true)} />
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
  if (item.kind === 'interrupted')
    return (
      <div class="compaction" role="separator">
        <span>{[item.text || 'Interrupted', when(item.at)].filter(Boolean).join(' · ')}</span>
      </div>
    );
  if (item.kind === 'round') {
    const note = roundNote(item.state);
    return (
      <>
        <div class="compaction" role="separator">
          <span>{roundLabel(item)}</span>
        </div>
        {note ? <p class="muted round-note">{note}</p> : null}
      </>
    );
  }
  if (item.kind === 'notice') return item.meta ? null : <div class="msg note">{item.text}</div>;
  return null;
}

const callId = (id: string) => id.split('#')[0];

// True for the first question item of its call among the blocks.
const callOf = (blocks: ReturnType<typeof blocksOf>, at: number, item: Item) => !blocks.slice(0, at).some((b) => b.kind === 'item' && b.item.kind === 'question' && callId(b.item.id) === callId(item.id));

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

export function AnswerCardView({ card, session, crew, openTerminal }: { card: AnswerCard; session: string; crew?: string; openTerminal?: () => void }) {
  // Which button was tapped; it stays busy until the card goes.
  const [tapped, setTapped] = useState<string | null>(null);
  const [gone, setGone] = useState<string | null>(null);
  // Set before the first re-render, so a double tap sends once.
  const sending = useRef(false);
  const key = cardKey(card);
  // A refused card must not hide the next prompt, which can share its key.
  useEffect(() => {
    sending.current = false;
    setTapped(null);
    setGone(null);
  }, [key]);
  if (card.kind === 'none' || gone === key) return null;
  if (card.kind === 'not_started')
    return (
      <Card title="This session hasn't started">
        <p class="line">
          It is waiting before its first step in <code>{card.dir}</code>, often at a prompt asking whether to trust the folder. Answer it in the terminal.
        </p>
        {openTerminal ? <Button label="Open terminal" onClick={openTerminal} /> : null}
      </Card>
    );
  if (card.kind === 'terminal')
    return (
      <Card title="Answer this in the terminal">
        {openTerminal ? <Button label="Open terminal" onClick={openTerminal} /> : null}
      </Card>
    );
  const send = async (choice: string, body: object) => {
    if (sending.current) return;
    sending.current = true;
    setTapped(choice);
    const res = await post('POST', `${shipUrl(session, crew)}/answer`, body, undefined, { timeoutMs: SLOW_TIMEOUT_MS });
    if (!res.ok) setGone(key);
  };
  if (card.kind === 'permission') {
    const tool = card.call?.tool;
    const input = tool?.input;
    return (
      <Card title={`${card.tool} needs your permission`}>
        {input ? <pre class="approve-input">{input}</pre> : null}
        {tool && (input ? tool.summary && tool.summary !== input : true) ? <p class="muted approve-note">{tool.summary ?? tool.name}</p> : null}
        <Actions class="card-actions">
          <Button label="Allow once" busy={tapped === 'allow'} pending={['ship', session, 'allow', crew ?? '']} onClick={() => send('allow', { card: 'permission', reason: card.reason, choice: 'allow' })} />
          <Button label="Deny" busy={tapped === 'deny'} pending={['ship', session, 'deny', crew ?? '']} onClick={() => send('deny', { card: 'permission', reason: card.reason, choice: 'deny' })} />
        </Actions>
      </Card>
    );
  }
  // A question is answered on its own transcript card.
  return null;
}

function ConversationBody({ conv, harness, anchored = false, working, noneTitle, footer, card, session, crew, openTerminal, loadOlder }: { conv: Conversation; harness: string; anchored?: boolean; working: boolean; noneTitle?: string; footer?: preact.ComponentChildren; card: AnswerCard; session: string; crew?: string; openTerminal?: () => void; loadOlder: () => void }) {
  const now = useTick(15_000);
  const scroller = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);
  const count = conv.items.length;

  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && pinned.current) el.scrollTop = el.scrollHeight;
  }, [count, working, conv.state]);

  if (conv.state === 'loading') return <p class="line">Loading…</p>;
  const terminal = openTerminal ? <Button label="Open terminal" onClick={openTerminal} /> : null;
  if (conv.state === 'none')
    return noneTitle ? (
      <Card title={noneTitle}>{terminal}</Card>
    ) : !hasConversationAdapter(harness) ? (
      <Card title="No conversation for this harness">{terminal}</Card>
    ) : anchored ? (
      <Card title="No conversation recorded">{terminal}</Card>
    ) : null;
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
        <Button label="Load earlier messages" class="convo-older" onClick={loadOlder} />
      ) : null}
      {blocks.length === 0 && !working ? <p class="line">No messages yet.</p> : null}
      {blocks.map((b, i) =>
        b.kind === 'item' && b.item.kind === 'question' ? (
          // The call's first question draws the whole call; the others are drawn with it.
          callOf(blocks, i, b.item) ? (
            <QuestionCall key={b.item.id} items={conv.items.filter((x) => x.kind === 'question' && callId(x.id) === callId(b.item.id))} card={card} session={session} crew={crew} openTerminal={openTerminal} />
          ) : null
        ) : b.kind === 'item' ? (
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
      {footer}
    </div>
  );
}

/** The conversation with the card that answers its prompt in place, always in the same position below it. */
export function ConversationView(props: { conv: Conversation; harness: string; anchored?: boolean; working: boolean; noneTitle?: string; footer?: preact.ComponentChildren; card: AnswerCard; session: string; crew?: string; openTerminal?: () => void; loadOlder: () => void }) {
  const { card, session, crew, openTerminal } = props;
  // A state that has its own Open terminal card needs no second one; a permission card needs no transcript.
  const shown: AnswerCard = card.kind === 'terminal' && props.conv.state !== 'ok' ? { kind: 'none' } : card;
  return (
    <>
      <ConversationBody conv={props.conv} harness={props.harness} anchored={props.anchored} working={props.working} noneTitle={shown.kind === 'not_started' ? undefined : props.noneTitle} footer={props.footer} card={shown} session={session} crew={crew} openTerminal={openTerminal} loadOlder={props.loadOlder} />
      <AnswerCardView card={shown} session={session} crew={crew} openTerminal={openTerminal} />
    </>
  );
}
