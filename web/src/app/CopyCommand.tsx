import { useRef, useState } from 'preact/hooks';
import { Button } from './Button';
import { copyOutcome } from './remoteTailscale';

export function CopyCommand({ command }: { command: string }) {
  const [state, setState] = useState<'copied' | 'select' | null>(null);
  const el = useRef<HTMLElement>(null);
  const copy = async () => {
    const outcome = await copyOutcome(command, navigator.clipboard);
    if (outcome === 'select' && el.current) {
      const range = document.createRange();
      range.selectNodeContents(el.current);
      getSelection()?.removeAllRanges();
      getSelection()?.addRange(range);
    }
    setState(outcome);
  };
  return (
    <div class="gh-cmd" role="group" aria-label={command}>
      <code ref={el}>{command}</code>
      <Button label="Copy" onClick={() => void copy()} />
      <span class="note" role="status">
        {state === 'copied' ? 'Copied.' : state === 'select' ? 'Select the command and copy it.' : ''}
      </span>
    </div>
  );
}
