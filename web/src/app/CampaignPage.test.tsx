import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/preact";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Fleet } from "./api";
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
let served: Campaign;
beforeEach(() => {
  posts = [];
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

test("clicking + toggles the Campaign issue field without leaving the page, and aria-expanded follows", async () => {
  await nav();
  const add = screen.getByRole("button", { name: "Add campaign" });
  expect(add.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByRole("textbox", { name: "Campaign issue" })).toBeNull();
  fireEvent.click(add);
  expect(screen.getByRole("textbox", { name: "Campaign issue" })).toBeTruthy();
  expect(add.getAttribute("aria-expanded")).toBe("true");
  expect(location.pathname).toBe("/app/ships");
  expect(location.search).toBe("?fleet=zulu");
  fireEvent.click(add);
  expect(screen.queryByRole("textbox", { name: "Campaign issue" })).toBeNull();
  expect(add.getAttribute("aria-expanded")).toBe("false");
  expect(location.pathname).toBe("/app/ships");
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
  expect(within(row).getByLabelText("2 ready, with you")).toBeTruthy();
});

test("a list card shows the same meta line and the same ready count as the sidebar row", () => {
  campaignList.value = [summary];
  render(<CampaignsNav />);
  const row = screen.getByRole("link", { name: /Widgets overhaul/ });
  const rowMeta = within(row).getByText(/acme\/widgets#20/).textContent;
  const rowReady = within(row).getByLabelText(/ready, with you/).getAttribute("aria-label");
  cleanup();
  render(<CampaignsList phone={false} title />);
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(within(card).getByText("acme/widgets#20 · 0 of 1").textContent).toBe(rowMeta);
  expect(card.textContent).not.toContain("Map");
  expect(within(card).getByLabelText(/ready, with you/).getAttribute("aria-label")).toBe(rowReady);
});

test("a list card shows its underway count with what it counts", () => {
  campaignList.value = [summary];
  render(<CampaignsList phone={false} title />);
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(within(card).getByLabelText("3 underway")).toBeTruthy();
});

test("a list card shows neither count when both are 0 or absent", () => {
  campaignList.value = [
    summaryOf({ ready_with_you: 0, counts: { ready: 0, underway: 0 } }),
    summaryOf({ id: "camp-2", title: "Gadgets", ready_with_you: 0, counts: {} }),
  ];
  render(<CampaignsList phone={false} title />);
  for (const name of [/Widgets overhaul/, /Gadgets/]) {
    const card = screen.getByRole("link", { name });
    expect(within(card).queryByLabelText(/ready, with you/)).toBeNull();
    expect(within(card).queryByLabelText(/underway/)).toBeNull();
  }
});

test("the Ended tab's cards carry the same counts", () => {
  campaignList.value = [summaryOf({ ended: true })];
  endedOpen.value = true;
  render(<CampaignsList phone={false} title />);
  const card = screen.getByRole("link", { name: /Widgets overhaul/ });
  expect(within(card).getByLabelText("2 ready, with you")).toBeTruthy();
  expect(within(card).getByLabelText("3 underway")).toBeTruthy();
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
  expect(await screen.findByRole("button", { name: /Map the parser/ })).toBeTruthy();
  const head = screen.getByRole("heading", { name: /^Done/ });
  expect(within(head).getByText("1")).toBeTruthy();
  expect(screen.queryByText("Shipped the lexer")).toBeNull();
  const toggle = within(head).getByRole("button");
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByText("Shipped the lexer")).toBeTruthy();
  expect(
    screen.getByRole("heading", { name: /^Ready, with you/ }).querySelector("button"),
  ).toBeNull();
});
