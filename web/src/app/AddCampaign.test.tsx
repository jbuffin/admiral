import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/preact";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { AddCampaign } from "./AddCampaign";
import { campaignList } from "./campaignsLink";
import { go } from "./router";

interface Row {
  title: string;
  reference: string;
  url: string;
  state: "open" | "closed";
  parent: boolean;
  progress: { closed: number; total: number };
  status: "" | "followed" | "unfollowed" | "ended";
}
const row = (
  over: Partial<Row> & { title: string; reference: string },
): Row => ({
  url: `https://github.com/${over.reference.split("#")[0]}/issues/${over.reference.split("#")[1]}`,
  state: "open",
  parent: false,
  progress: { closed: 0, total: 0 },
  status: "",
  ...over,
});
const widgets = row({
  title: "Widgets overhaul",
  reference: "acme/widgets#20",
  parent: true,
  progress: { closed: 1, total: 4 },
});
const gadgets = row({
  title: "Gadgets plan",
  reference: "acme/gadgets#7",
  parent: true,
  progress: { closed: 0, total: 2 },
  status: "followed",
});
const leaf = row({
  title: "Fix the widget hinge",
  reference: "acme/widgets#31",
});
const old = row({
  title: "Old widget bug",
  reference: "acme/widgets#5",
  state: "closed",
});

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });
const followed = {
  id: "camp-9",
  fleet: "zulu",
  title: "Widgets overhaul",
  progress: { closed: 1, total: 4 },
  counts: {},
  ended: false,
};

let searches: string[];
let follows: Record<string, unknown>[];
// What the stub answers for a search with this text; a function lets a test hold an answer back.
let answer: (q: string) => Response | Promise<Response>;
let follow: () => Response | Promise<Response>;
let onClose: ReturnType<typeof vi.fn>;

beforeEach(() => {
  searches = [];
  follows = [];
  answer = () => json({ results: [] });
  follow = () => json(followed, 201);
  onClose = vi.fn();
  campaignList.value = [];
  go("/app/campaigns", "?fleet=zulu");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init: RequestInit = {}) => {
      const url = new URL(path, "http://admiral.test");
      const route = url.pathname.replace("/api/v1", "");
      if (route === "/fleets/zulu/campaigns/search") {
        const q = url.searchParams.get("q") ?? "";
        searches.push(q);
        return answer(q);
      }
      if (route === "/campaigns" && init.method === "POST") {
        follows.push(JSON.parse(String(init.body)));
        return follow();
      }
      throw new Error(`unstubbed ${init.method ?? "GET"} ${path}`);
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const opened = () => render(<AddCampaign fleet="zulu" onClose={onClose} />);
const input = () =>
  screen.findByPlaceholderText("Search, #n, owner/repo#n or a URL");
const type = async (text: string) =>
  fireEvent.input(await input(), { target: { value: text } });

test("before typing it lists the fleet's open parents under their caption", async () => {
  answer = () => json({ results: [widgets] });
  opened();
  expect(
    await screen.findByText("Open parent issues in this fleet"),
  ).toBeTruthy();
  const pick = await screen.findByRole("button", { name: /Widgets overhaul/ });
  expect(within(pick).getByText("acme/widgets#20")).toBeTruthy();
  expect(within(pick).getByText("1/4")).toBeTruthy();
  expect(searches[0]).toBe("");
});

test("the search input has focus when it opens", async () => {
  opened();
  expect(document.activeElement).toBe(await input());
});

test("results list parents under Parent issues before the rest under Other issues, a closed issue says closed and only a parent has a pill", async () => {
  answer = () => json({ results: [widgets, leaf, old] });
  opened();
  await type("widget");
  const parents = await screen.findByText("Parent issues");
  const others = await screen.findByText("Other issues");
  expect(
    parents.compareDocumentPosition(others) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  const pick = (name: RegExp) => screen.getByRole("button", { name });
  expect(
    parents.compareDocumentPosition(pick(/Widgets overhaul/)) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(
    others.compareDocumentPosition(pick(/Fix the widget hinge/)) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(within(pick(/Old widget bug/)).getByText(/closed/)).toBeTruthy();
  expect(
    within(pick(/Fix the widget hinge/)).queryByText(/\d+\/\d+/),
  ).toBeNull();
});

test("a followed row says Following and can't be picked", async () => {
  answer = () => json({ results: [gadgets, widgets] });
  opened();
  const mine = await screen.findByRole("button", { name: /Gadgets plan/ });
  expect(within(mine).getByText("Following")).toBeTruthy();
  expect((mine as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(mine);
  expect(follows).toEqual([]);
  expect(
    (
      screen.getByRole("button", {
        name: /Widgets overhaul/,
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
});

test("picking a row follows its reference in the modal's fleet and opens the campaign", async () => {
  answer = () => json({ results: [widgets] });
  opened();
  fireEvent.click(
    await screen.findByRole("button", { name: /Widgets overhaul/ }),
  );
  await waitFor(() => expect(location.pathname).toBe("/app/campaigns/camp-9"));
  expect(follows).toEqual([{ reference: "acme/widgets#20", fleet: "zulu" }]);
  expect(location.search).toBe("?fleet=zulu");
});

test("a refused follow shows its reason and keeps the modal open", async () => {
  answer = () => json({ results: [widgets] });
  follow = () => json({ error: "acme/widgets belongs to fleet yankee" }, 422);
  opened();
  fireEvent.click(
    await screen.findByRole("button", { name: /Widgets overhaul/ }),
  );
  expect(
    await screen.findByText("acme/widgets belongs to fleet yankee"),
  ).toBeTruthy();
  expect(location.pathname).toBe("/app/campaigns");
  expect(onClose).not.toHaveBeenCalled();
  expect(await input()).toBeTruthy();
});

test("a refused search shows its reason", async () => {
  answer = () => json({ error: "gh is signed out" }, 422);
  opened();
  expect(await screen.findByText("gh is signed out")).toBeTruthy();
});

test("no result says so", async () => {
  opened();
  await type("zzz");
  expect(await screen.findByText(/matches/i)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Widgets/ })).toBeNull();
});

test("a slow answer for older input never overwrites the newer one", async () => {
  const held: Record<string, (r: Response) => void> = {};
  answer = (q) =>
    q === ""
      ? json({ results: [] })
      : new Promise((resolve) => (held[q] = resolve));
  opened();
  await type("wid");
  await waitFor(() => expect(searches).toContain("wid"));
  await type("widget");
  await waitFor(() => expect(searches).toContain("widget"));
  held.widget(json({ results: [leaf] }));
  expect(
    await screen.findByRole("button", { name: /Fix the widget hinge/ }),
  ).toBeTruthy();
  const stale = json({ results: [widgets] });
  const read = stale.text.bind(stale);
  let consumed!: Promise<string>;
  stale.text = () => (consumed = read());
  held.wid(stale);
  await waitFor(() => expect(consumed).toBeDefined());
  await consumed;
  await act(async () => {});
  expect(screen.queryByRole("button", { name: /Widgets overhaul/ })).toBeNull();
  expect(
    screen.getByRole("button", { name: /Fix the widget hinge/ }),
  ).toBeTruthy();
});

test("Escape and a backdrop tap leave the modal open while a follow is in flight", async () => {
  answer = () => json({ results: [widgets] });
  let release!: (r: Response) => void;
  follow = () => new Promise((resolve) => (release = resolve));
  opened();
  fireEvent.click(
    await screen.findByRole("button", { name: /Widgets overhaul/ }),
  );
  await waitFor(() => expect(follows).toHaveLength(1));
  const dialog = screen.getByRole("dialog", { name: "Add campaign" });
  fireEvent.keyDown(dialog, { key: "Escape" });
  fireEvent.click(dialog.parentElement!);
  expect(onClose).not.toHaveBeenCalled();
  release(json(followed, 201));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
});

test("Start a new campaign is the last row and disabled", async () => {
  answer = () => json({ results: [widgets] });
  opened();
  await screen.findByRole("button", { name: /Widgets overhaul/ });
  const buttons = screen.getAllByRole("button");
  const start = screen.getByRole("button", { name: /Start a new campaign/ });
  expect((start as HTMLButtonElement).disabled).toBe(true);
  expect(buttons[buttons.length - 1]).toBe(start);
});

test("Escape and a tap on the backdrop close it, a tap inside doesn't", async () => {
  opened();
  const dialog = await screen.findByRole("dialog", { name: "Add campaign" });
  fireEvent.click(dialog);
  expect(onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(dialog, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
  fireEvent.click(dialog.parentElement!);
  expect(onClose).toHaveBeenCalledTimes(2);
});
