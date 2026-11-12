import { useEffect, useRef, useState } from 'preact/hooks';
import { call } from './api';
import type { ChatSnapshot } from './chat';
import { chats } from './shipsLink';
import { type AnswerBody, type Draft, blankDraft } from '../questions';

const clean = (s: ChatSnapshot): ChatSnapshot => ({ ...s, messages: s.messages ?? [], questions: s.questions ?? [] });

/** One fleet's chat: its snapshot, the composer's text, each question's draft and the actions. Opening it starts the session; the server leaves a running one alone. */
export function useChat(fleet: string) {
  const [snap, setSnap] = useState<ChatSnapshot | null>(null);
  const [error, setError] = useState('');
  const [text, setText] = useState('');
  const [sending, setSending] = useState(false);
  const [resetting, setResetting] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const alive = useRef(true);
  const base = `/chat/${encodeURIComponent(fleet)}`;

  const reread = async () => {
    const r = await call<ChatSnapshot>('GET', `${base}/session`);
    if (r.ok && alive.current) setSnap(clean(r.data));
  };

  useEffect(() => {
    alive.current = true;
    void (async () => {
      await reread();
      const r = await call('POST', `${base}/start`);
      if (alive.current && !r.ok) setError(r.error);
      await reread();
    })();
    return () => {
      alive.current = false;
    };
  }, [fleet]);

  const pushed = chats.value[fleet];
  useEffect(() => {
    if (pushed) setSnap(clean(pushed));
  }, [pushed]);

  const send = async () => {
    const typed = text;
    if (!typed.trim() || sending) return;
    setSending(true);
    setError('');
    const r = await call('POST', `${base}/messages`, { text: typed });
    setSending(false);
    if (!r.ok) return setError(r.error);
    // Keep only what was typed while the request was out, so a second Enter doesn't send the text again.
    setText((now) => (now.startsWith(typed) ? now.slice(typed.length) : now));
  };

  const newChat = async () => {
    if (resetting) return;
    setResetting(true);
    setError('');
    const r = await call('POST', `${base}/new`);
    setResetting(false);
    if (!r.ok) return setError(r.error);
    setText('');
    setDrafts({});
    await reread();
  };

  const editDraft = (id: string, fn: (d: Draft) => Draft) => setDrafts((all) => ({ ...all, [id]: fn(all[id] ?? blankDraft()) }));

  const answer = async (id: string, body: AnswerBody) => {
    if (drafts[id]?.sending) return;
    editDraft(id, (d) => ({ ...d, sending: true, error: '' }));
    const r = await call('POST', `${base}/questions/${encodeURIComponent(id)}/answer`, body);
    // A 409 or 404 means the question was answered elsewhere or withdrawn; the snapshot says which, and the socket doesn't push for it.
    if (r.ok || r.status === 409 || r.status === 404) await reread();
    editDraft(id, (d) => ({ ...d, sending: false, error: r.ok ? '' : r.error }));
  };

  return { snap, error, text, setText, sending, resetting, drafts, editDraft, send, newChat, answer };
}
