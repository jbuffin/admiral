import { Button } from './Button';
import { commodoreNote, type SkillsAnswer } from './launchForm';

export interface CommodoreRun {
  harness: string;
  action: 'install' | 'update';
  // `refreshing` is the skills read that follows a good run, so the stale offer stays busy until it lands.
  status: 'running' | 'refreshing' | 'failed';
  error?: string;
  output?: string;
  commands?: string[];
}

interface Props {
  commodore: SkillsAnswer['commodore'];
  /** The run for the dock the dialog is on, if any. */
  run: CommodoreRun | null;
  /** What a good run said beside its state. */
  warning: string | null;
  onRun: (action: 'install' | 'update') => void;
}

export function LaunchCommodoreOffer({ commodore, run, warning, onRun }: Props) {
  const busy = run?.status === 'running' || run?.status === 'refreshing';
  // The version line is moot while the update runs.
  const note = busy ? null : commodoreNote(commodore);
  const action = commodore.state === 'missing' ? 'install' : commodore.state === 'outdated' ? 'update' : null;
  if (!action && !note && !run && !warning) return null;
  return (
    <div class="commodore-offer">
      {note ? <p class="note">{note}</p> : null}
      {commodore.state === 'unknown-version' ? <p class="note">To switch, remove the loose skills, then Install.</p> : null}
      {action ? (
        <div>
          <Button label={action === 'install' ? 'Install Commodore' : 'Update'} busyLabel={action === 'install' ? 'Installing Commodore…' : 'Updating…'} busy={busy} onClick={() => onRun(action)} />
        </div>
      ) : null}
      {warning ? <p class="note warn">{warning}</p> : null}
      {run?.status === 'failed' ? (
        <>
          <p class="note warn">{run.error}</p>
          {run.output ? <pre class="commodore-output">{run.output}</pre> : null}
          {run.commands?.length ? <p class="note">To run it by hand:</p> : null}
          {run.commands?.map((c) => (
            <code key={c} class="commodore-command">{c}</code>
          ))}
        </>
      ) : null}
    </div>
  );
}
