import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/preact";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Fleet, Ship } from "./api";
import type { Campaign, CampaignNode } from "./campaign";
import {
  addOpen,
  CampaignPage,
  CampaignsList,
  CampaignsNav,
} from "./CampaignPage";
import { campaignList, endedOpen } from "./campaignsLink";
import { fleets } from "./fleets";
import { go } from "./router";
import { resources, ships } from "./shipsLink";

const rootUrl = "https://github.com/acme/widgets/issues/20";
const childUrl = "https://github.com/acme/widgets/issues/21";
const fleet = {
  key: "zulu",
  name: "Westwind",
  color: "#336699",
  ink: "#fff",
  docks: [{ harness: "claude" }],
} as unknown as Fleet;

const none = { kind: "none", name: "", args: "" } as const;
// The root and the child both disagree with their labels, so a Mode choice and Use labels would show wherever they are offered.
const child: CampaignNode = {
  number: 21,
  title: "Map the parser",
  url: childUrl,
  repo: "acme/widgets",
  status: "ready",
  mode: "with-you",
  skill: none,
  default_mode: "without-you",
  default_skill: none,
  overridden: true,
  labels: ["ready-for-agent"],
};
const root: CampaignNode = {
  number: 20,
  title: "Widgets overhaul",
  url: rootUrl,
  repo: "acme/widgets",
  parent: true,
  status: "ready",
  mode: "with-you",
  skill: none,
  default_mode: "without-you",
  default_skill: none,
  overridden: true,
  labels: ["wayfinder:map"],
  children: [child],
};
const campaign: Campaign = {
  id: "camp-1",
  url: rootUrl,
  fleet: "zulu",
  work_repo: "",
  ended: false,
  last_read: "",
  read_error: "",
  progress: { closed: 0, total: 1 },
  counts: { ready: 1 },
  title: "Widgets overhaul",
  number: 20,
  repo: "acme/widgets",
  followed_at: "",
  ready_with_you: 1,
  ready_without_you: 0,
  reading: false,
  root,
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });
const proj = "/home/jb/proj";

let posts: Record<string, unknown>[];
let follows: Record<string, unknown>[];
let served: Campaign;
beforeEach(() => {
  posts = [];
  follows = [];
  served = campaign;
  campaignList.value = [];
  endedOpen.value = false;
  fleets.value = [fleet];
  ships.value = [];
  resources.value = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const route = new URL(path, "http://admiral.test").pathname.replace(
        "/api/v1",
        "",
      );
      if (route === "/campaigns/camp-1" && (init.method ?? "GET") === "GET")
        return json(served);
      if (route === "/campaigns" && (init.method ?? "GET") === "GET")
        return json({ campaigns: [served] });
      if (route === "/campaigns" && init.method === "POST") {
        follows.push(JSON.parse(String(init.body)));
        served = { ...served, ended: false, ended_at: undefined, end_reason: undefined } as Campaign;
        return json(served);
      }
      if (route === "/fleets/zulu/campaigns/search")
        return json({ results: [] });
      if (route === "/launch-orders/dirs")
        return json({
          entries: [
            { path: proj, name: "proj", kind: "checkout", ships_here: 0 },
          ],
          selected: proj,
          clone: null,
        });
      if (route === "/launch-orders/skills")
        return json({
          mark: "/",
          commodore: { found: false, name: "commodore", state: "missing" },
          skills: [],
        });
      if (route === "/launch-orders/crew")
        return json({ members: [], warnings: [], errors: [] });
      if (route === "/campaigns/camp-1/launch" && init.method === "POST") {
        posts.push(JSON.parse(String(init.body)));
        return json({ ship: { session: "zulu-1" } }, 201);
      }
      throw new Error(`unstubbed ${init.method ?? "GET"} ${path}`);
    }),
  );
});
afterEach(() => {
  cleanup();
  addOpen.value = false;
  campaignList.value = [];
  vi.unstubAllGlobals();
});

const opened = async () => {
  render(<CampaignPage id="camp-1" title />);
  return screen.findByRole("button", { name: "Widgets overhaul" });
};

test("tapping the campaign's title opens the root's sheet with what a child's shows, its mode and skill as plain text, and no Mode choice or Use labels", async () => {
  fireEvent.click(await opened());
  const sheet = await screen.findByRole("dialog", { name: "Widgets overhaul" });
  expect(within(sheet).getByText(/acme\/widgets#20 · Ready/)).toBeTruthy();
  expect(within(sheet).getByText("wayfinder:map")).toBeTruthy();
  expect(
    within(sheet)
      .getByRole("link", { name: "Open the issue on GitHub" })
      .getAttribute("href"),
  ).toBe(rootUrl);
  expect(within(sheet).getByText(/With you · Just launch/)).toBeTruthy();
  expect(within(sheet).getByRole("button", { name: "Launch" })).toBeTruthy();
  expect(within(sheet).queryByRole("radiogroup", { name: "Mode" })).toBeNull();
  expect(within(sheet).queryByRole("radio")).toBeNull();
  expect(
    within(sheet).queryByRole("button", { name: "Use labels" }),
  ).toBeNull();
});

test("Launch on the root posts the root's own URL as child_url", async () => {
  fireEvent.click(await opened());
  fireEvent.click(await screen.findByRole("button", { name: "Launch" }));
  await waitFor(() =>
    expect((screen.getByLabelText("Checkout") as HTMLSelectElement).value).toBe(
      proj,
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Launch" }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({
    child_url: rootUrl,
    harness: "claude",
    dir: proj,
  });
});

test("a child's sheet still offers the Mode choice and Use labels", async () => {
  render(<CampaignPage id="camp-1" title />);
  fireEvent.click(
    await screen.findByRole("button", { name: /Map the parser/ }),
  );
  const sheet = await screen.findByRole("dialog", { name: /Map the parser/ });
  expect(within(sheet).getByRole("radiogroup", { name: "Mode" })).toBeTruthy();
  expect(
    within(sheet).getByRole("button", { name: "Use labels" }),
  ).toBeTruthy();
});

const nav = async () => {
  go("/app/ships", "?fleet=zulu");
  campaignList.value = [
    { ...campaign, id: "camp-1" },
    { ...campaign, id: "camp-2", title: "Old plan", ended: true },
  ];
  addOpen.value = false;
  render(<CampaignsNav />);
};

test("the sidebar shows one Campaigns row, a link with the Add campaign button beside it, and no second Campaigns label", async () => {
  await nav();
  const links = screen.getAllByRole("link", { name: "Campaigns" });
  expect(links).toHaveLength(1);
  expect(links[0].getAttribute("href")).toBe("/app/campaigns?fleet=zulu");
  expect(screen.getAllByRole("button", { name: "Add campaign" })).toHaveLength(
    1,
  );
  expect(screen.getAllByText("Campaigns")).toHaveLength(1);
  expect(
    links[0].contains(screen.getByRole("button", { name: "Add campaign" })),
  ).toBe(false);
});

test("clicking the Campaigns link opens the Campaigns page", async () => {
  await nav();
  fireEvent.click(screen.getByRole("link", { name: "Campaigns" }));
  expect(location.pathname).toBe("/app/campaigns");
  expect(location.search).toBe("?fleet=zulu");
});

test("clicking + opens the Add campaign modal without leaving the page, and the paste field is gone", async () => {
  await nav();
  expect(screen.queryByRole("dialog", { name: "Add campaign" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add campaign" }));
  expect(
    await screen.findByRole("dialog", { name: "Add campaign" }),
  ).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "Campaign issue" })).toBeNull();
  expect(location.pathname).toBe("/app/ships");
  expect(location.search).toBe("?fleet=zulu");
});

test("the phone's Campaigns list has an Add campaign button that opens the modal, and no paste field", async () => {
  campaignList.value = [];
  fleets.value = [fleet];
  go("/app/campaigns", "?fleet=zulu");
  render(<CampaignsList phone title />);
  expect(screen.queryByRole("textbox", { name: "Campaign issue" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Add campaign" }));
  expect(
    await screen.findByRole("dialog", { name: "Add campaign" }),
  ).toBeTruthy();
  expect(location.pathname).toBe("/app/campaigns");
});

test("an empty Following list points to Add campaign", () => {
  render(<CampaignsList phone={false} title />);
  expect(screen.getByText(/Add campaign/)).toBeTruthy();
  expect(screen.queryByText(/pasting/)).toBeNull();
});

test("the followed campaigns and Ended (n) still show under the row", async () => {
  await nav();
  expect(screen.getByRole("link", { name: /Widgets overhaul/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Ended (1)" })).toBeTruthy();
});

const summary: Campaign = {
  ...campaign,
  repo: "https://github.com/acme/widgets",
  progress: { closed: 0, total: 1 },
  counts: { ready: 2, underway: 3 },
  ready_with_you: 2,
};
const summaryOf = (over: Partial<Campaign>) => ({ ...summary, ...over });

test("the sidebar row names the repo and issue, never Map, and says what its badge counts", () => {
  campaignList.value = [summary];
  render(<CampaignsNav />);
  const row = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(within(row).getByText("acme/widgets#20 · 0 of 1")).toBeTruthy();
  expect(row.textContent).not.toContain("Map");
  expect(screen.getByRole("link", { name: /2 ready, with you/ })).toBeTruthy();
});

test("a list card shows the same meta line and the same ready count as the sidebar row", () => {
  campaignList.value = [summary];
  render(<CampaignsNav />);
  const row = screen.getByRole("link", { name: /Widgets overhaul/ });
  const rowMeta = within(row).getByText(/acme\/widgets#20/).textContent;
  expect(row.getAttribute("href")).toBeTruthy();
  expect(screen.getByRole("link", { name: /2 ready, with you/ })).toBe(row);
  cleanup();
  render(<CampaignsList phone={false} title />);
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(within(card).getByText("acme/widgets#20 · 0 of 1").textContent).toBe(
    rowMeta,
  );
  expect(card.textContent).not.toContain("Map");
  expect(screen.getByRole("link", { name: /2 ready, with you/ })).toBe(card);
});

test("a list card shows its underway count with what it counts", () => {
  campaignList.value = [summary];
  render(<CampaignsList phone={false} title />);
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(screen.getByRole("link", { name: /3 underway/ })).toBe(card);
});

test("a list card shows neither count when both are 0 or absent", () => {
  campaignList.value = [
    summaryOf({ ready_with_you: 0, counts: { ready: 0, underway: 0 } }),
    summaryOf({
      id: "camp-2",
      title: "Gadgets",
      ready_with_you: 0,
      counts: {},
    }),
  ];
  render(<CampaignsList phone={false} title />);
  for (const name of [/Widgets overhaul/, /Gadgets/]) {
    const card = screen.getByRole("link", { name });
    expect(card.textContent).not.toMatch(/ready, with you|underway/);
  }
});

test("the Ended tab's cards carry the same counts", () => {
  campaignList.value = [summaryOf({ ended: true })];
  endedOpen.value = true;
  render(<CampaignsList phone={false} title />);
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(card.getAttribute("href")).toBeTruthy();
  expect(
    screen.getByRole("link", { name: /2 ready, with you 3 underway/ }),
  ).toBe(card);
});

test("on a campaign's page Done shows its count with its rows hidden, and tapping its head shows them", async () => {
  const shipped: CampaignNode = {
    ...child,
    number: 22,
    title: "Shipped the lexer",
    url: "https://github.com/acme/widgets/issues/22",
    status: "done",
  };
  served = {
    ...campaign,
    progress: { closed: 1, total: 2 },
    root: { ...root, children: [child, shipped] },
  };
  render(<CampaignPage id="camp-1" title />);
  expect(
    await screen.findByRole("button", { name: /Map the parser/ }),
  ).toBeTruthy();
  const head = screen.getByRole("heading", { name: /^Done/ });
  expect(within(head).getByText("1")).toBeTruthy();
  expect(screen.queryByText("Shipped the lexer")).toBeNull();
  const toggle = within(head).getByRole("button");
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByText("Shipped the lexer")).toBeTruthy();
  expect(
    screen
      .getByRole("heading", { name: /^Ready, with you/ })
      .querySelector("button"),
  ).toBeNull();
});

const unfollowed = () =>
  ({ ...campaign, ended: true, ended_at: "2026-01-01T12:00:00Z", end_reason: "unfollowed" }) as Campaign;
const treeClosed = () =>
  ({ ...campaign, ended: true, ended_at: "2026-01-01T12:00:00Z", end_reason: "ended" }) as Campaign;

test("an unfollowed campaign offers Follow again and no Unfollow, and following again returns it to the sidebar", async () => {
  go("/app/ships", "?fleet=zulu");
  served = unfollowed();
  campaignList.value = [served];
  render(
    <>
      <CampaignsNav />
      <CampaignPage id="camp-1" title />
    </>,
  );
  const again = await screen.findByRole("button", { name: "Follow again" });
  expect(screen.queryByRole("button", { name: "Unfollow" })).toBeNull();
  expect(screen.queryByRole("link", { name: /Widgets overhaul/ })).toBeNull();

  fireEvent.click(again);

  await waitFor(() => expect(follows).toHaveLength(1));
  expect(follows[0]).toMatchObject({ reference: rootUrl });
  expect(await screen.findByRole("link", { name: /Widgets overhaul/ })).toBeTruthy();
  expect(await screen.findByRole("button", { name: "Unfollow" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Follow again" })).toBeNull();
});

test("a campaign that ended because its tree closed shows Ended and no Follow again", async () => {
  served = treeClosed();
  render(<CampaignPage id="camp-1" title />);
  await screen.findByRole("button", { name: "Widgets overhaul" });
  await waitFor(() => expect(screen.getByText("Ended")).toBeTruthy());
  expect(screen.queryByRole("button", { name: "Follow again" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Unfollow" })).toBeNull();
});

const drafter = (over: Record<string, unknown> = {}) =>
  ({
    session: "saltpaw",
    name: "Saltpaw",
    title: "Plan the parser rewrite",
    fleet: "zulu",
    harness: "claude",
    status: "underway",
    skill: "wayfinder",
    skill_kind: "skill",
    drafting: true,
    crew: [],
    ...over,
  }) as unknown as Ship;
const draftingRow = () =>
  Array.from(document.querySelectorAll("a, button")).find((el) =>
    /Drafting a campaign/.test(el.textContent ?? ""),
  ) as HTMLElement | undefined;
const draftingRows = () =>
  Array.from(document.querySelectorAll("a, button")).filter((el) =>
    /Drafting a campaign/.test(el.textContent ?? ""),
  );

test("the sidebar shows a Drafting row for a drafting ship of the fleet, with its skill and ship name, above the followed campaigns", async () => {
  ships.value = [drafter()];
  await nav();
  const row = draftingRow()!;
  expect(row.textContent).toMatch(
    /^\s*Drafting a campaign\s*·\s*\/wayfinder\s*·\s*Saltpaw\s*$/,
  );
  expect(row.textContent).not.toContain("Plan the parser rewrite");
  const followed = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(
    row.compareDocumentPosition(followed) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
});

test("the Drafting row shows a ship's name and skill as text, never as markup", async () => {
  ships.value = [drafter({ name: "<b>Salt</b> *paw*", skill: "<i>way</i>_*x*" })];
  await nav();
  const row = draftingRow()!;
  expect(row.textContent).toContain("<b>Salt</b> *paw*");
  expect(row.textContent).toContain("/<i>way</i>_*x*");
  expect(row.querySelector("b, i, em")).toBeNull();
});

test("a drafting ship of another fleet, and a ship that is not drafting, show no Drafting row", async () => {
  ships.value = [
    drafter({ session: "other", fleet: "alpha" }),
    drafter({ session: "plain", drafting: undefined }),
  ];
  await nav();
  expect(draftingRows()).toHaveLength(0);
});

test("the Drafting row goes when the ship drops drafting, and the followed campaign shows, with no reload", async () => {
  ships.value = [drafter()];
  campaignList.value = [];
  go("/app/ships", "?fleet=zulu");
  render(<CampaignsNav />);
  expect(draftingRow()).toBeTruthy();
  expect(screen.queryByRole("link", { name: /Widgets overhaul/ })).toBeNull();
  ships.value = [drafter({ drafting: undefined })];
  campaignList.value = [{ ...campaign, id: "camp-1" }];
  await waitFor(() => expect(draftingRow()).toBeUndefined());
  expect(screen.getByRole("link", { name: /Widgets overhaul/ })).toBeTruthy();
});

test("the Drafting row goes when the ship is gone", async () => {
  ships.value = [drafter()];
  await nav();
  expect(draftingRow()).toBeTruthy();
  ships.value = [];
  await waitFor(() => expect(draftingRow()).toBeUndefined());
});

test("tapping the Drafting row opens the ship's bridge", async () => {
  ships.value = [drafter()];
  await nav();
  fireEvent.click(draftingRow()!);
  expect(location.pathname).toBe("/app/ships/saltpaw");
});

test("a moored drafting ship keeps its Drafting row", async () => {
  ships.value = [drafter({ status: "moored" })];
  await nav();
  expect(draftingRows()).toHaveLength(1);
});

test("the phone's Campaigns list shows the Drafting row above the cards and drops the empty line while one shows", () => {
  ships.value = [drafter()];
  campaignList.value = [];
  go("/app/campaigns", "?fleet=zulu");
  render(<CampaignsList phone title />);
  expect(draftingRows()).toHaveLength(1);
  expect(screen.queryByText(/Follow a campaign with Add campaign/)).toBeNull();
  cleanup();
  campaignList.value = [{ ...campaign, id: "camp-1" }];
  render(<CampaignsList phone title />);
  const row = draftingRow()!;
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(
    row.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
});
