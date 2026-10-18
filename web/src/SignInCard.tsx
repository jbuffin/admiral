import { useRef, useState } from 'preact/hooks';
import type { Auth } from './sources';

const isWebUrl = (u?: string) => !!u && /^https?:\/\//i.test(u);

// Admiral is plain HTTP on the LAN, where navigator.clipboard does not exist on
// the iPad, so the code is selected where it is and copied in the tap handler.
export function SignInCard({ auth }: { auth: Auth }) {
  const code = useRef<HTMLElement>(null);
  const [copied, setCopied] = useState(false);
  const copy = () => {
    const el = code.current;
    const sel = getSelection();
    if (!el || !sel) return;
    const range = document.createRange();
    range.selectNodeContents(el);
    sel.removeAllRanges();
    sel.addRange(range);
    setCopied(document.execCommand('copy'));
  };
  return (
    <div class="signin">
      <strong>Sign in</strong>
      <p>{auth.message}</p>
      {isWebUrl(auth.url) && (
        <a href={auth.url} target="_blank" rel="noopener noreferrer">
          {auth.url}
        </a>
      )}
      {auth.code && (
        <div class="signin-code">
          <code ref={code}>{auth.code}</code>
          <button type="button" class="small-button press" onClick={copy}>
            Copy code
          </button>
          {copied && <span aria-live="polite">Copied</span>}
        </div>
      )}
    </div>
  );
}
