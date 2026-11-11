export type SurfaceId = 'inbox' | 'ships' | 'chat' | 'campaigns' | 'routines' | 'crew' | 'preferences';

export interface Surface {
  id: SurfaceId;
  title: string;
  /** Names the active fleet. The inbox spans every fleet; Preferences is global for now. */
  scoped: boolean;
  line: string;
}

// In nav order.
export const SURFACES: Surface[] = [
  { id: 'inbox', title: 'Inbox', scoped: false, line: 'Whatever needs you, from every fleet, will land here.' },
  { id: 'ships', title: 'Ships', scoped: true, line: 'The ships sailing from this fleet will appear here.' },
  { id: 'chat', title: 'Chat', scoped: true, line: "This fleet's AdmiralBot will live here." },
  { id: 'campaigns', title: 'Campaigns', scoped: true, line: 'Campaigns for this fleet will be planned and followed here.' },
  { id: 'routines', title: 'Routines', scoped: true, line: 'The routines this fleet runs on a schedule will live here.' },
  { id: 'crew', title: 'Crew', scoped: true, line: 'The crew members of this fleet will live here.' },
  { id: 'preferences', title: 'Preferences', scoped: false, line: 'Settings for Admiral and each fleet will live here.' },
];

export const PHONE_TABS: SurfaceId[] = ['inbox', 'chat', 'ships'];
export const MORE: SurfaceId[] = ['campaigns', 'routines', 'crew', 'preferences'];
export const surfaceById = (id: SurfaceId) => SURFACES.find((s) => s.id === id)!;
