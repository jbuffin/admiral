import { effect, signal } from '@preact/signals';
import { call } from './api';
import type { Campaign } from './campaign';
import { rememberFleet } from './fleets';
import { campaignPath, go } from './router';
import { ships } from './shipsLink';

export const campaignList = signal<Campaign[]>([]);
export const campaignsLoaded = signal(false);
// The Campaigns list shows the ended campaigns instead of the followed ones.
export const endedOpen = signal(false);

export async function refreshCampaigns() {
  const r = await call<{ campaigns: Campaign[] | null }>('GET', '/campaigns');
  if (r.ok) campaignList.value = r.data.campaigns ?? [];
  campaignsLoaded.value = true;
}

/** Puts a campaign the page just read or changed into the list, so the sidebar doesn't wait for the next refresh. */
export function noteCampaign(c: Campaign) {
  const { root: _tree, ...summary } = c;
  const at = campaignList.value.findIndex((x) => x.id === c.id);
  campaignList.value = at < 0 ? [...campaignList.value, summary] : campaignList.value.map((x, i) => (i === at ? summary : x));
}

/** Follows an issue and opens its page, in the campaign's own fleet; answers the refusal, or null. */
export async function followReference(reference: string): Promise<string | null> {
  const r = await call<Campaign>('POST', '/campaigns', { reference });
  if (!r.ok) return r.error;
  noteCampaign(r.data);
  rememberFleet(r.data.fleet);
  go(campaignPath(r.data.id), `?fleet=${encodeURIComponent(r.data.fleet)}`);
  return null;
}

/** Keeps the list fresh: now, every minute, and shortly after the ships change. */
export function watchCampaigns() {
  void refreshCampaigns();
  setInterval(() => void refreshCampaigns(), 60_000);
  let later: ReturnType<typeof setTimeout> | undefined;
  effect(() => {
    void ships.value;
    clearTimeout(later);
    later = setTimeout(() => void refreshCampaigns(), 800);
  });
}
