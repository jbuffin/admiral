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
import type { DirsAnswer, Fleet } from "./api";
import { AddCampaign } from "./AddCampaign";
import { capabilities, capabilitiesLoaded } from "./capabilities";
import { campaignList } from "./campaignsLink";
import { MAX_PROSE_RUNES } from "./filing";
import { fleets } from "./fleets";
import type { SkillsAnswer } from "./launchForm";
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
let drafts: Record<string, unknown>[];
let creates: Record<string, unknown>[];
let draftAnswer: () => Response | Promise<Response>;
let createAnswer: () => Response | Promise<Response>;
// What the fleet's capabilities say about its judge; a null harness is drafting off.
let judge: { harness: string | null; reason?: string };
let repos: { path: string; name: string; kind: string; remotes: string[] }[];
let sources: { key: string; plugin: string; fleet: string; name: string }[];
let accounts: Record<string, { account: string; host?: string }>;
// The drafting-skill reads and writes; log holds each one in the order the modal made it.
interface Drafting {
  skill: string;
  harnesses: { harness: string; names: string[] }[];
}
let log: string[];
let dirsBodies: Record<string, unknown>[];
let saves: Record<string, unknown>[];
let ships: Record<string, unknown>[];
let dirsFor: (harness: string) => DirsAnswer | Promise<DirsAnswer>;
let draftingFor: (dir: string) => Drafting;
let skillsFor: (
  harness: string,
  dir: string,
) => SkillsAnswer | Promise<SkillsAnswer>;
let shipAnswer: () => Response | Promise<Response>;
let saveAnswer: () => Response | Promise<Response>;

const checkout = (name: string, ...remotes: string[]) => ({
  path: `/work/${name}`,
  name,
  kind: "checkout",
  remotes,
});
const dirEntry = (name: string, kind = "checkout") => ({
  path: `/work/${name}`,
  name,
  kind,
  ships_here: 0,
  remotes: [],
});
const skillsOf = (mark: string, ...names: string[]): SkillsAnswer => ({
  mark,
  commodore: { found: false, name: "commodore", state: "missing" },
  skills: names.map((name) => ({ name, description: "", favorite: false })),
});
const github = (key: string, fleet = "zulu") => ({
  key,
  plugin: "github",
  fleet,
  name: key,
});

beforeEach(() => {
  searches = [];
  follows = [];
  drafts = [];
  creates = [];
  draftAnswer = () =>
    json({ title: "Pool: sensor work", body: "Restates the idea." });
  createAnswer = () => json(followed, 201);
  judge = { harness: "claude" };
  repos = [
    checkout("widgets", "github.com/acme/widgets"),
    checkout("gadgets", "github.com/acme/gadgets"),
  ];
  sources = [github("github")];
  accounts = { github: { account: "octo-work" } };
  capabilities.value = [];
  capabilitiesLoaded.value = false;
  fleets.value = [];
  log = [];
  dirsBodies = [];
  saves = [];
  ships = [];
  dirsFor = (harness) => ({
    entries:
      harness === "codex"
        ? [dirEntry("tools")]
        : [dirEntry("widgets"), dirEntry("gadgets")],
    selected: harness === "codex" ? "/work/tools" : "/work/widgets",
    clone: null,
  });
  draftingFor = () => ({ skill: "", harnesses: [] });
  skillsFor = (harness) =>
    skillsOf(harness === "codex" ? "$" : "/", "wayfinder", "review");
  shipAnswer = () => json({ ship: { session: "zulu-7" } }, 201);
  saveAnswer = () => json({}, 200);
  localStorage.clear();
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
      if (route === "/fleets/zulu/campaigns/draft" && init.method === "POST") {
        drafts.push(JSON.parse(String(init.body)));
        return draftAnswer();
      }
      if (route === "/campaigns/create" && init.method === "POST") {
        creates.push(JSON.parse(String(init.body)));
        log.push("create");
        return createAnswer();
      }
      if (route === "/launch-orders/dirs" && init.method === "POST") {
        const body = JSON.parse(String(init.body));
        dirsBodies.push(body);
        log.push(`dirs:${body.harness}`);
        return json(await dirsFor(body.harness));
      }
      if (
        route === "/fleets/zulu/drafting" &&
        (init.method ?? "GET") === "GET"
      ) {
        const dir = url.searchParams.get("dir") ?? "";
        log.push(`drafting:${dir}`);
        return json(draftingFor(dir));
      }
      if (route === "/fleets/zulu/drafting" && init.method === "PUT") {
        saves.push(JSON.parse(String(init.body)));
        log.push("save");
        return saveAnswer();
      }
      if (route === "/launch-orders/skills") {
        const harness = url.searchParams.get("harness") ?? "";
        const dir = url.searchParams.get("dir") ?? "";
        log.push(`skills:${harness}:${dir}`);
        return json(await skillsFor(harness, dir));
      }
      if (route === "/ships" && init.method === "POST") {
        ships.push(JSON.parse(String(init.body)));
        log.push("ship");
        return shipAnswer();
      }
      if (route === "/fleets/zulu/repos") return json({ repos });
      if (route === "/harness-capabilities")
        return json({
          fleets: [{ key: "zulu", name: "Zulu", harnesses: [], judge }],
        });
      if (route === "/sources")
        return json(
          sources.map((s) => ({
            ...s,
            icon_url: "",
            has_schema: true,
            state: "ok",
            error: null,
          })),
        );
      const settings = /^\/sources\/([^/]+)\/settings$/.exec(route);
      if (settings && accounts[settings[1]])
        return json({ name: settings[1], settings: accounts[settings[1]] });
      throw new Error(`unstubbed ${init.method ?? "GET"} ${path}`);
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
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

test("an unfollowed row can be picked, and picking it follows its reference again", async () => {
  const dropped = { ...widgets, status: "unfollowed" as const };
  answer = () => json({ results: [dropped] });
  follow = () => json(followed, 200);
  opened();
  const mine = await screen.findByRole("button", { name: /Widgets overhaul/ });
  expect((mine as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(mine);
  await waitFor(() => expect(location.pathname).toBe("/app/campaigns/camp-9"));
  expect(follows).toEqual([{ reference: "acme/widgets#20", fleet: "zulu" }]);
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

test("Start a new campaign is the last row, and with text typed it reads the text", async () => {
  answer = () => json({ results: [widgets] });
  opened();
  await screen.findByRole("button", { name: /Widgets overhaul/ });
  const buttons = screen.getAllByRole("button");
  const start = screen.getByRole("button", { name: /Start a new campaign/ });
  expect((start as HTMLButtonElement).disabled).toBe(false);
  expect(buttons[buttons.length - 1]).toBe(start);
  await type("pool work");
  expect(
    await screen.findByRole("button", {
      name: /^Start a new campaign: 'pool work'$/,
    }),
  ).toBeTruthy();
});

const startRow = () =>
  screen.getByRole("button", { name: /^Start a new campaign/ });
const textArea = () =>
  screen.findByRole("textbox", { name: "What is this campaign for?" });
const repoSelect = () =>
  screen.findByRole("combobox", {
    name: /Repo for the parent issue/,
  }) as Promise<HTMLSelectElement>;
const titleBox = () =>
  screen.findByRole("textbox", { name: "Title" }) as Promise<HTMLInputElement>;
const bodyBox = () =>
  screen.findByRole("textbox", {
    name: "Description",
  }) as Promise<HTMLTextAreaElement>;
// An option reads as the repo, with a host in front only when it isn't github.com.
const label = (o: Element) =>
  (o.textContent ?? "").trim().replace(/^github\.com\//, "");
const chosen = (sel: HTMLSelectElement) => label(sel.selectedOptions[0]);
const offered = (sel: HTMLSelectElement) =>
  within(sel).getAllByRole("option").map(label).sort();
const choose = (sel: HTMLSelectElement, repo: string) => {
  const o = within(sel)
    .getAllByRole("option")
    .find((x) => label(x) === repo) as HTMLOptionElement;
  fireEvent.change(sel, { target: { value: o.value } });
};
// The modal reads the judge's capabilities when the text step opens, so Draft or Next is settled by then.
const settled = () =>
  waitFor(() => expect(capabilitiesLoaded.value).toBe(true));
const toTextStep = async (typed = "pool work") => {
  await type(typed);
  await screen.findByRole("button", { name: /^Start a new campaign/ });
  fireEvent.click(startRow());
  return textArea();
};
const toConfirm = async (typed = "pool work") => {
  await toTextStep(typed);
  await settled();
  fireEvent.click(
    await screen.findByRole("button", { name: /^(Draft|Next)$/ }),
  );
  return repoSelect();
};

test.each([
  ["a bare number", "#12"],
  ["a repo and number", "acme/widgets#12"],
  ["an issue URL", "https://github.com/acme/widgets/issues/12"],
])(
  "Start a new campaign opens the text step empty when what was typed is %s",
  async (_name, ref) => {
    opened();
    const area = (await toTextStep(ref)) as HTMLTextAreaElement;
    expect(area.value).toBe("");
  },
);

test("Start a new campaign opens the text step seeded with the typed text", async () => {
  opened();
  const area = (await toTextStep(
    "pool work for acme/widgets",
  )) as HTMLTextAreaElement;
  expect(area.value).toBe("pool work for acme/widgets");
  expect(
    screen.queryByPlaceholderText("Search, #n, owner/repo#n or a URL"),
  ).toBeNull();
});

test("Back returns to search with the search text intact, and the text area keeps its own words for the return trip", async () => {
  opened();
  const area = (await toTextStep("pool work")) as HTMLTextAreaElement;
  fireEvent.input(area, { target: { value: "pool work, reworded" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(((await input()) as HTMLInputElement).value).toBe("pool work");
  expect(
    screen.queryByRole("textbox", { name: "What is this campaign for?" }),
  ).toBeNull();
  fireEvent.click(startRow());
  expect(((await textArea()) as HTMLTextAreaElement).value).toBe(
    "pool work, reworded",
  );
});

test("Draft sends the text to the fleet's draft route and opens the confirm step with the drafter's title and description tagged Drafted", async () => {
  opened();
  await toConfirm("pool work");
  expect(drafts).toEqual([{ text: "pool work" }]);
  expect((await titleBox()).value).toBe("Pool: sensor work");
  expect((await bodyBox()).value).toBe("Restates the idea.");
  expect(screen.getAllByText("Drafted")).toHaveLength(2);
  fireEvent.input(await titleBox(), { target: { value: "Pool work" } });
  expect(screen.getAllByText("Drafted")).toHaveLength(1);
});

test("with drafting off the button reads Next, makes no draft call, and the confirm step opens with the text as the description and an empty title", async () => {
  judge = { harness: null, reason: "no harness is proven to run the judge" };
  opened();
  await toTextStep("pool work");
  expect(await screen.findByRole("button", { name: "Next" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Draft" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Next" }));
  expect((await bodyBox()).value).toBe("pool work");
  expect((await titleBox()).value).toBe("");
  expect(drafts).toEqual([]);
  expect(screen.queryByText(/Couldn't draft/)).toBeNull();
  expect(screen.queryByText("Drafted")).toBeNull();
  // Create waits for a title, then files the engineer's own words.
  const create = screen.getByRole("button", {
    name: "Create campaign",
  }) as HTMLButtonElement;
  expect(create.disabled).toBe(true);
  fireEvent.input(await titleBox(), { target: { value: "Pool work" } });
  fireEvent.click(create);
  await waitFor(() => expect(creates).toHaveLength(1));
  expect(creates[0]).toMatchObject({
    title: "Pool work",
    body: "pool work",
    issues: [],
  });
});

test("a draft the server refuses with 409 goes the way drafting off does, with no failure line", async () => {
  draftAnswer = () =>
    json({ error: "drafting is off", reason: "no judge" }, 409);
  opened();
  await toConfirm("pool work");
  expect((await bodyBox()).value).toBe("pool work");
  expect((await titleBox()).value).toBe("");
  expect(screen.queryByText(/Couldn't draft/)).toBeNull();
  expect(screen.queryByText("Drafted")).toBeNull();
});

test("a failed draft opens the confirm step with the text as the description, an empty title and the reason", async () => {
  draftAnswer = () => json({ error: "the judge timed out" }, 502);
  opened();
  await toConfirm("pool work");
  expect(
    await screen.findByText(
      "Couldn't draft a title: the judge timed out. Write your own.",
    ),
  ).toBeTruthy();
  expect((await bodyBox()).value).toBe("pool work");
  expect((await titleBox()).value).toBe("");
  expect(screen.queryByText("Drafted")).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: "Create campaign",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

test("the repo select offers one choice per remote of the fleet's checkouts on a host the fleet has a GitHub source for, and starts on the first by name", async () => {
  repos = [
    checkout("widgets", "github.com/acme/widgets", "github.com/fork/widgets"),
    checkout("tools", "ghe.example.com/acme/tools"),
    checkout("other", "gitlab.com/acme/other"),
    {
      path: "/work",
      name: "work",
      kind: "search_root",
      remotes: ["github.com/acme/root"],
    },
    checkout("gadgets", "github.com/acme/gadgets"),
  ];
  sources = [github("github"), github("ghe"), github("elsewhere", "yankee")];
  accounts = {
    github: { account: "octo-work" },
    ghe: { account: "octo-ghe", host: "ghe.example.com" },
    elsewhere: { account: "octo-other", host: "gitlab.com" },
  };
  opened();
  const sel = await toConfirm();
  expect(offered(sel)).toEqual(
    [
      "acme/gadgets",
      "acme/widgets",
      "fork/widgets",
      "ghe.example.com/acme/tools",
    ].sort(),
  );
  choose(sel, "acme/gadgets");
  expect(chosen(sel)).toBe("acme/gadgets");
});

test("the repo select starts on the first project by name when nothing is remembered", async () => {
  repos = [
    checkout("zeta", "github.com/acme/zeta"),
    checkout("widgets", "github.com/acme/widgets"),
    checkout("gadgets", "github.com/acme/gadgets"),
  ];
  opened();
  expect(chosen(await toConfirm())).toBe("acme/gadgets");
});

test("the repo select starts on the repo this device last filed a campaign in while it is still a project, else on the first by name", async () => {
  repos = [
    checkout("zeta", "github.com/acme/zeta"),
    checkout("widgets", "github.com/acme/widgets"),
    checkout("gadgets", "github.com/acme/gadgets"),
  ];
  opened();
  const first = await toConfirm();
  choose(first, "acme/zeta");
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  expect(creates[0]).toMatchObject({ repo: "acme/zeta" });
  cleanup();

  opened();
  expect(chosen(await toConfirm())).toBe("acme/zeta");
  cleanup();

  repos = repos.filter((r) => r.name !== "zeta");
  opened();
  expect(chosen(await toConfirm())).toBe("acme/gadgets");
});

test("a browser that blocks storage still shows the confirm step and files the campaign", async () => {
  const blocked = () => {
    throw new DOMException("blocked", "SecurityError");
  };
  // happy-dom caches a storage method after its first read, so a prototype spy depends on test order; the global is replaced instead.
  vi.stubGlobal("localStorage", { getItem: blocked, setItem: blocked });
  expect(() => localStorage.getItem("x")).toThrow();
  opened();
  expect(chosen(await toConfirm())).toBe("acme/gadgets");
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
});

test("the confirm step names the account that files it, for the chosen repo's host", async () => {
  repos = [
    checkout("widgets", "github.com/acme/widgets"),
    checkout("tools", "ghe.example.com/acme/tools"),
  ];
  sources = [github("github"), github("ghe")];
  accounts = {
    github: { account: "octo-work" },
    ghe: { account: "octo-ghe", host: "ghe.example.com" },
  };
  opened();
  const sel = await toConfirm();
  choose(sel, "acme/widgets");
  expect(await screen.findByText(/@octo-work/)).toBeTruthy();
  choose(sel, "ghe.example.com/acme/tools");
  expect(await screen.findByText(/@octo-ghe/)).toBeTruthy();
  expect(screen.queryByText(/@octo-work/)).toBeNull();
});

test("Create campaign files the parent with no issues in the chosen repo, its host and this fleet, then closes the modal and opens the campaign", async () => {
  repos = [
    checkout("widgets", "github.com/acme/widgets"),
    checkout("tools", "ghe.example.com/acme/tools"),
  ];
  sources = [github("github"), github("ghe")];
  accounts = {
    github: { account: "octo-work" },
    ghe: { account: "octo-ghe", host: "ghe.example.com" },
  };
  opened();
  const sel = await toConfirm("pool work");
  choose(sel, "ghe.example.com/acme/tools");
  fireEvent.input(await titleBox(), { target: { value: "Pool work" } });
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  await waitFor(() => expect(location.pathname).toBe("/app/campaigns/camp-9"));
  expect(creates).toEqual([
    {
      repo: "acme/tools",
      host: "ghe.example.com",
      fleet: "zulu",
      title: "Pool work",
      body: "Restates the idea.",
      issues: [],
    },
  ]);
  expect(follows).toEqual([]);
  expect(location.search).toBe("?fleet=zulu");
  expect(onClose).toHaveBeenCalledTimes(1);
});

test("a refused create shows the server's reason and keeps the modal open for another try", async () => {
  createAnswer = () =>
    json({ error: "acme/widgets belongs to fleet yankee" }, 422);
  opened();
  await toConfirm();
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  expect(
    await screen.findByText("acme/widgets belongs to fleet yankee"),
  ).toBeTruthy();
  expect(onClose).not.toHaveBeenCalled();
  expect(location.pathname).toBe("/app/campaigns");
  expect(
    (
      screen.getByRole("button", {
        name: "Create campaign",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(false);
});

test("a create that filed the parent but couldn't follow it links the parent and can't be sent again", async () => {
  const parent = "https://github.com/acme/gadgets/issues/50";
  createAnswer = () =>
    json({ error: "follow failed: gh is signed out", parent }, 502);
  opened();
  await toConfirm();
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  expect(
    await screen.findByText(/follow failed: gh is signed out/),
  ).toBeTruthy();
  const link = screen.getByRole("link", { name: parent });
  expect(link.getAttribute("href")).toBe(parent);
  expect(
    (
      screen.getByRole("button", {
        name: "Create campaign",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(onClose).not.toHaveBeenCalled();
});

test("a return to the confirm step after a create that filed the parent can't file a second", async () => {
  const parent = "https://github.com/acme/gadgets/issues/50";
  createAnswer = () =>
    json({ error: "follow failed: gh is signed out", parent }, 502);
  opened();
  await toConfirm();
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  await screen.findByRole("link", { name: parent });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.click(await screen.findByRole("button", { name: "Draft" }));
  await repoSelect();
  expect(screen.getByRole("link", { name: parent })).toBeTruthy();
  const create = screen.getByRole("button", {
    name: "Create campaign",
  }) as HTMLButtonElement;
  expect(create.disabled).toBe(true);
  fireEvent.click(create);
  expect(creates).toHaveLength(1);
});

test("the text step seeds from new search text after a reference left it empty", async () => {
  opened();
  const empty = (await toTextStep("#12")) as HTMLTextAreaElement;
  expect(empty.value).toBe("");
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  await type("pool work");
  await screen.findByRole("button", {
    name: /^Start a new campaign: 'pool work'$/,
  });
  fireEvent.click(startRow());
  expect(((await textArea()) as HTMLTextAreaElement).value).toBe("pool work");
});

test("Escape still closes the modal after Start a new campaign, on the text step and the confirm step", async () => {
  opened();
  await toTextStep("pool work");
  const dialog = screen.getByRole("dialog", { name: "Add campaign" });
  expect(dialog.contains(document.activeElement)).toBe(true);
  fireEvent.keyDown(document.activeElement ?? document.body, {
    key: "Escape",
    bubbles: true,
  });
  expect(onClose).toHaveBeenCalledTimes(1);
  await settled();
  fireEvent.click(await screen.findByRole("button", { name: "Draft" }));
  await repoSelect();
  expect(dialog.contains(document.activeElement)).toBe(true);
  fireEvent.keyDown(document.activeElement ?? document.body, {
    key: "Escape",
    bubbles: true,
  });
  expect(onClose).toHaveBeenCalledTimes(2);
});

test("Escape and a tap on the backdrop close it, a tap inside doesn\'t", async () => {
  opened();
  const dialog = await screen.findByRole("dialog", { name: "Add campaign" });
  fireEvent.click(dialog);
  expect(onClose).not.toHaveBeenCalled();
  fireEvent.keyDown(dialog, { key: "Escape" });
  expect(onClose).toHaveBeenCalledTimes(1);
  fireEvent.click(dialog.parentElement!);
  expect(onClose).toHaveBeenCalledTimes(2);
});

// The drafting skill: the Add campaign modal's new-campaign step can hand the text to a drafting ship.
const docked = (...harnesses: string[]) =>
  (fleets.value = [
    {
      key: "zulu",
      docks: harnesses.map((harness) => ({
        harness,
        executable: harness,
        executable_found: true,
        warnings: [],
      })),
    } as unknown as Fleet,
  ]);
const installedOn = (
  ...docks: [string, ...string[]][]
): Drafting["harnesses"] =>
  docks.map(([harness, ...names]) => ({ harness, names }));
const wayfinder = "wayfinder";
const withSaved = (skill: string, ...docks: [string, ...string[]][]) =>
  (draftingFor = () => ({ skill, harnesses: installedOn(...docks) }));
const skillBox = () =>
  screen.findByRole("combobox", {
    name: "Drafting skill",
  }) as Promise<HTMLSelectElement>;
const optionLabels = (sel: HTMLSelectElement) =>
  within(sel)
    .getAllByRole("option")
    .map((o) => (o.textContent ?? "").trim());
const selectedLabel = (sel: HTMLSelectElement) =>
  (sel.selectedOptions[0].textContent ?? "").trim();
const pickOption = (sel: HTMLSelectElement, start: string) => {
  const o = within(sel)
    .getAllByRole("option")
    .find((x) =>
      (x.textContent ?? "").trim().startsWith(start),
    ) as HTMLOptionElement;
  fireEvent.change(sel, { target: { value: o.value } });
};
// The deepest element holding the Ship line, so a wrapped label or separator can't hide its words.
const shipLine = () =>
  screen.findByText((_, el) => {
    const own = (el?.textContent ?? "").replace(/\s+/g, " ").trim();
    const deeper = [...(el?.children ?? [])].some((c) =>
      /^Ship:.*own worktree/.test(
        (c.textContent ?? "").replace(/\s+/g, " ").trim(),
      ),
    );
    return /^Ship:.*own worktree/.test(own) && !deeper;
  });
const noShipLine = () => expect(screen.queryByText(/^Ship:/)).toBeNull();
const launch = async () =>
  (await screen.findByRole("button", {
    name: "Launch drafting ship",
  })) as HTMLButtonElement;
const dockRadios = () =>
  within(screen.getByRole("group", { name: "Dock" })).getAllByRole("radio");
const checkoutBox = () =>
  screen.getByRole("combobox", { name: "Checkout" }) as HTMLSelectElement;
// Both reads that follow a dir are in the log once the skills read for it is.
const readFor = (harness: string, dir: string) =>
  waitFor(() => {
    expect(log).toContain(`drafting:${dir}`);
    expect(log).toContain(`skills:${harness}:${dir}`);
  });
const toSkillStep = async (typed = "pool work") => {
  const area = await toTextStep(typed);
  await settled();
  return area as HTMLTextAreaElement;
};
// The name of an ambiguity choice may be a radio or an option; either way it lists the installed names.
const chooseName = (name: string) => {
  const exact = new RegExp(`^${name}$`);
  const radio = screen.queryByRole("radio", { name: exact });
  if (radio) return fireEvent.click(radio);
  const o = screen
    .getAllByRole("option")
    .find((x) => exact.test((x.textContent ?? "").trim())) as HTMLOptionElement;
  fireEvent.change(o.parentElement!, { target: { value: o.value } });
};
const hasName = (name: string) =>
  screen.queryByRole("radio", { name: new RegExp(`^${name}$`) }) ??
  screen
    .queryAllByRole("option")
    .find((x) => (x.textContent ?? "").trim() === name) ??
  null;

test("the Drafting skill select opens on the fleet's saved skill, with its Ship line and Launch drafting ship in place of Draft", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep();
  const sel = await skillBox();
  await waitFor(() =>
    expect(selectedLabel(sel)).toBe(
      "/wayfinder: a drafting ship runs it with you",
    ),
  );
  expect(optionLabels(sel)).toContain(
    "No skill: Admiral drafts a title and description",
  );
  expect(await shipLine()).toBeTruthy();
  expect((await launch()).disabled).toBe(false);
  expect(screen.queryByRole("button", { name: "Draft" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
});

test("with nothing saved the select opens on No skill, with Draft and no Ship line", async () => {
  docked("claude");
  opened();
  await toSkillStep();
  const sel = await skillBox();
  await readFor("claude", "/work/widgets");
  expect(selectedLabel(sel)).toBe(
    "No skill: Admiral drafts a title and description",
  );
  expect(optionLabels(sel)).toContain(
    "/wayfinder: a drafting ship runs it with you",
  );
  expect(screen.getByRole("button", { name: "Draft" })).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Launch drafting ship" }),
  ).toBeNull();
  noShipLine();
});

test("the first read asks for the first dock's directories without the text, starts on its last-used checkout, then reads the drafting skill and the dock's skills for it", async () => {
  docked("claude", "codex");
  dirsFor = () => ({
    entries: [dirEntry("widgets"), dirEntry("gadgets")],
    selected: "/work/gadgets",
    clone: null,
  });
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep("pool work for acme/widgets");
  const line = await shipLine();
  expect(line.textContent).toMatch(/claude/i);
  expect(line.textContent).toContain("gadgets");
  expect(dirsBodies[0]).toEqual({ fleet: "zulu", harness: "claude" });
  await readFor("claude", "/work/gadgets");
  expect(log[0]).toBe("dirs:claude");
});

test("when the last-used directory is a search root the repo starts on the first checkout", async () => {
  docked("claude");
  dirsFor = () => ({
    entries: [
      dirEntry("work", "search_root"),
      dirEntry("gadgets"),
      dirEntry("widgets"),
    ],
    selected: "/work/work",
    clone: null,
  });
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep();
  expect((await shipLine()).textContent).toContain("gadgets");
  await readFor("claude", "/work/gadgets");
});

test("a saved skill installed only on the second dock starts the Ship line there, re-reading directories, drafting and skills once for it", async () => {
  docked("claude", "codex");
  withSaved(wayfinder, ["codex", wayfinder]);
  opened();
  await toSkillStep();
  await waitFor(async () => {
    const line = (await shipLine()).textContent;
    expect(line).toMatch(/codex/i);
    expect(line).toContain("tools");
  });
  await readFor("codex", "/work/tools");
  expect(dirsBodies).toEqual([
    { fleet: "zulu", harness: "claude" },
    { fleet: "zulu", harness: "codex" },
  ]);
  expect(log.filter((l) => l.startsWith("drafting:"))).toHaveLength(2);
  expect(log.filter((l) => l.startsWith("skills:"))).toHaveLength(2);
  expect((await launch()).disabled).toBe(false);
});

test("Change shows the dock picker limited to the docks that carry the skill and a checkout picker of checkouts only, without clone", async () => {
  docked("claude", "codex", "copilot");
  dirsFor = () => ({
    entries: [
      dirEntry("widgets"),
      dirEntry("work", "search_root"),
      dirEntry("gadgets"),
    ],
    selected: "/work/widgets",
    clone: { repo: "acme/widgets", path: "/work/new" },
  });
  withSaved(wayfinder, ["claude", wayfinder], ["codex", wayfinder]);
  opened();
  await toSkillStep();
  await shipLine();
  expect(screen.queryByRole("combobox", { name: "Checkout" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  const docks = dockRadios().map((r) =>
    r.closest("label")?.textContent?.trim().toLowerCase(),
  );
  expect(docks).toEqual(["claude", "codex"]);
  expect(
    within(checkoutBox())
      .getAllByRole("option")
      .map((o) => (o.textContent ?? "").trim())
      .sort(),
  ).toEqual(["gadgets (checkout)", "widgets (checkout)"]);
});

test("picking another repo in Change re-reads the drafting skill and the skills for it", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep();
  await shipLine();
  await readFor("claude", "/work/widgets");
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  fireEvent.change(checkoutBox(), { target: { value: "/work/gadgets" } });
  await readFor("claude", "/work/gadgets");
  expect((await shipLine()).textContent).toContain("gadgets");
  expect(dirsBodies).toHaveLength(1);
});

test("picking another harness in Change moves the repo to that dock's last-used checkout and re-reads directories, drafting and skills", async () => {
  docked("claude", "codex");
  withSaved(wayfinder, ["claude", wayfinder], ["codex", wayfinder]);
  opened();
  await toSkillStep();
  await shipLine();
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  const codex = dockRadios().find((r) =>
    /codex/i.test(r.closest("label")?.textContent ?? ""),
  )!;
  fireEvent.click(codex);
  await readFor("codex", "/work/tools");
  expect(dirsBodies.at(-1)).toEqual({ fleet: "zulu", harness: "codex" });
  await waitFor(async () => {
    const line = (await shipLine()).textContent;
    expect(line).toMatch(/codex/i);
    expect(line).toContain("tools");
  });
});

test("a skill that isn't installed on the Ship line's harness stays selected, marks the option and the Ship line not installed, and blocks Launch drafting ship until No skill is picked", async () => {
  docked("claude");
  withSaved(wayfinder);
  opened();
  await toSkillStep();
  const sel = await skillBox();
  await waitFor(() =>
    expect(selectedLabel(sel)).toMatch(/wayfinder.*not installed/i),
  );
  expect((await shipLine()).textContent).toMatch(/not installed/i);
  expect((await launch()).disabled).toBe(true);
  expect(ships).toEqual([]);
  pickOption(sel, "No skill");
  const draft = (await screen.findByRole("button", {
    name: "Draft",
  })) as HTMLButtonElement;
  expect(draft.disabled).toBe(false);
  noShipLine();
});

test("a repo that lacks the skill marks the Ship line not installed without switching the harness", async () => {
  docked("claude", "codex");
  draftingFor = (dir) => ({
    skill: wayfinder,
    harnesses:
      dir === "/work/widgets"
        ? installedOn(["claude", wayfinder], ["codex", wayfinder])
        : [],
  });
  opened();
  await toSkillStep();
  await shipLine();
  await readFor("claude", "/work/widgets");
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  fireEvent.change(checkoutBox(), { target: { value: "/work/gadgets" } });
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Launch drafting ship",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true),
  );
  const line = await shipLine();
  expect(line.textContent).toMatch(/not installed/i);
  expect(line.textContent).toMatch(/claude/i);
  expect(dirsBodies).toHaveLength(1);
});

test("a dock listing two installed names asks which one, and Launch drafting ship waits for the answer then sends it and saves it", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder, "acme:wayfinder"]);
  opened();
  await toSkillStep("pool work");
  await shipLine();
  await waitFor(() => expect(hasName("acme:wayfinder")).toBeTruthy());
  expect(hasName(wayfinder)).toBeTruthy();
  const button = await launch();
  expect(button.disabled).toBe(true);
  fireEvent.click(button);
  expect(ships).toEqual([]);
  chooseName("acme:wayfinder");
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Launch drafting ship",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(screen.getByRole("button", { name: "Launch drafting ship" }));
  await waitFor(() => expect(ships).toHaveLength(1));
  expect(ships[0]).toMatchObject({
    skill: { kind: "skill", name: "acme:wayfinder" },
  });
  await waitFor(() => expect(saves).toEqual([{ skill: "acme:wayfinder" }]));
});

test("installed plugin skills are listed once by their plain name", async () => {
  docked("claude");
  skillsFor = () =>
    skillsOf("/", "acme:wayfinder", "other:wayfinder", "review");
  opened();
  await toSkillStep();
  const sel = await skillBox();
  await readFor("claude", "/work/widgets");
  await waitFor(() => expect(optionLabels(sel)).toHaveLength(3));
  expect(optionLabels(sel).sort()).toEqual(
    [
      "No skill: Admiral drafts a title and description",
      "/review: a drafting ship runs it with you",
      "/wayfinder: a drafting ship runs it with you",
    ].sort(),
  );
});

test.each(["ghost", "acme:ghost"])(
  "a saved skill (%s) that no dock has installed still has its option, selected and marked not installed",
  async (name) => {
    docked("claude");
    withSaved(name);
    opened();
    await toSkillStep();
    const sel = await skillBox();
    await waitFor(() => expect(selectedLabel(sel)).toContain(name));
    expect(selectedLabel(sel)).toMatch(/not installed/i);
  },
);

test("Launch drafting ship sends the drafting launch with the skill, harness, directory and text, then saves the pick, closes the modal and opens the ship's bridge", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep("pool work");
  await shipLine();
  fireEvent.click(await launch());
  await waitFor(() => expect(location.pathname).toBe("/app/ships/zulu-7"));
  expect(ships).toEqual([
    {
      drafting: true,
      fleet: "zulu",
      harness: "claude",
      dir: "/work/widgets",
      text: "pool work",
      skill: { kind: "skill", name: wayfinder },
    },
  ]);
  expect(location.search).toBe("?fleet=zulu");
  expect(onClose).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(saves).toEqual([{ skill: wayfinder }]));
  expect(log.indexOf("save")).toBeGreaterThan(log.indexOf("ship"));
});

test("choosing another skill in the select saves nothing until its launch succeeds, then saves its plain name", async () => {
  docked("claude");
  skillsFor = () => skillsOf("/", "wayfinder", "acme:review");
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep();
  const sel = await skillBox();
  await shipLine();
  pickOption(sel, "/review");
  expect(saves).toEqual([]);
  fireEvent.click(await launch());
  await waitFor(() => expect(ships).toHaveLength(1));
  expect(ships[0]).toMatchObject({ skill: { kind: "skill", name: "review" } });
  await waitFor(() => expect(saves).toEqual([{ skill: "review" }]));
});

test("a refused drafting launch shows its reason, keeps the modal open and saves nothing", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  shipAnswer = () =>
    json({ error: "no installed skill carries wayfinder" }, 400);
  opened();
  await toSkillStep();
  await shipLine();
  fireEvent.click(await launch());
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("no installed skill carries wayfinder");
  expect(onClose).not.toHaveBeenCalled();
  expect(location.pathname).toBe("/app/campaigns");
  expect(saves).toEqual([]);
});

test("a failed save of the pick doesn't undo the launch", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  saveAnswer = () => json({ error: "config.json is busy" }, 409);
  opened();
  await toSkillStep();
  await shipLine();
  fireEvent.click(await launch());
  await waitFor(() => expect(location.pathname).toBe("/app/ships/zulu-7"));
  await waitFor(() => expect(saves).toHaveLength(1));
  expect(onClose).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("alert")).toBeNull();
});

test("Launch drafting ship is disabled while the text is empty or over the limit", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  const area = await toSkillStep("pool work");
  await shipLine();
  expect((await launch()).disabled).toBe(false);
  fireEvent.input(area, { target: { value: "" } });
  expect((await launch()).disabled).toBe(true);
  fireEvent.input(area, { target: { value: "x".repeat(MAX_PROSE_RUNES + 1) } });
  expect((await launch()).disabled).toBe(true);
  expect(ships).toEqual([]);
});

test("Launch drafting ship is disabled while the reads are loading", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  let release!: (a: SkillsAnswer) => void;
  skillsFor = () => new Promise((resolve) => (release = resolve));
  opened();
  await toSkillStep();
  await waitFor(() =>
    expect(log.some((l) => l.startsWith("skills:"))).toBe(true),
  );
  const button = await launch();
  expect(button.disabled).toBe(true);
  fireEvent.click(button);
  expect(ships).toEqual([]);
  release(skillsOf("/", wayfinder));
  await waitFor(async () => expect((await launch()).disabled).toBe(false));
});

test("Create campaign on the No skill path saves an empty skill after the campaign is filed, and Draft alone saves nothing", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  opened();
  await toSkillStep();
  pickOption(await skillBox(), "No skill");
  fireEvent.click(await screen.findByRole("button", { name: "Draft" }));
  await repoSelect();
  expect(saves).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  expect(creates).toHaveLength(1);
  await waitFor(() => expect(saves).toEqual([{ skill: "" }]));
  expect(log.indexOf("save")).toBeGreaterThan(log.indexOf("create"));
});

test("Back from the confirm step, closing, and a failed create save nothing", async () => {
  docked("claude");
  withSaved(wayfinder, ["claude", wayfinder]);
  createAnswer = () =>
    json({ error: "acme/widgets belongs to fleet yankee" }, 422);
  opened();
  await toSkillStep();
  pickOption(await skillBox(), "No skill");
  fireEvent.click(await screen.findByRole("button", { name: "Draft" }));
  await repoSelect();
  fireEvent.click(screen.getByRole("button", { name: "Create campaign" }));
  await screen.findByText("acme/widgets belongs to fleet yankee");
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  await skillBox();
  fireEvent.keyDown(screen.getByRole("dialog", { name: "Add campaign" }), {
    key: "Escape",
  });
  expect(onClose).toHaveBeenCalledTimes(1);
  await act(async () => {});
  expect(saves).toEqual([]);
});

test("Back from the confirm step keeps the pick, the Ship line's harness and its repo", async () => {
  docked("claude", "codex");
  withSaved(wayfinder, ["claude", wayfinder], ["codex", wayfinder]);
  opened();
  await toSkillStep();
  await shipLine();
  fireEvent.click(screen.getByRole("button", { name: "Change" }));
  fireEvent.click(
    dockRadios().find((r) =>
      /codex/i.test(r.closest("label")?.textContent ?? ""),
    )!,
  );
  await readFor("codex", "/work/tools");
  const sel = await skillBox();
  pickOption(sel, "No skill");
  fireEvent.click(await screen.findByRole("button", { name: "Draft" }));
  await repoSelect();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  const back = await skillBox();
  expect(selectedLabel(back)).toBe(
    "No skill: Admiral drafts a title and description",
  );
  pickOption(back, "/wayfinder");
  await waitFor(async () => {
    const line = (await shipLine()).textContent;
    expect(line).toMatch(/codex/i);
    expect(line).toContain("tools");
  });
});
