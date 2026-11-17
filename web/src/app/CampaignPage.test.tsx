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
import { CampaignPage } from "./CampaignPage";
import { fleets } from "./fleets";
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
beforeEach(() => {
  posts = [];
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
        return json(campaign);
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
