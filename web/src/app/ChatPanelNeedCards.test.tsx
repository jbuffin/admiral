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
import { ChatPanel } from "./ChatPanel";
import { fleets } from "./fleets";
import type { InboxItem } from "./inbox";
import { loc } from "./router";
import { inboxItems, ships } from "./shipsLink";
import type { Surface } from "./surfaces";

// Need cards for the five kinds beyond a ship's question: choices, answer bodies, collapsed states and errors.

const surface = {
  id: "chat",
  title: "Chat",
  scoped: true,
  line: "",
} as unknown as Surface;
const alpha = {
  key: "alpha",
  name: "Homeport",
  color: "#2a9d8f",
  docks: [],
  chat: {
    on: true,
    off_reason: "",
    harnesses: [],
    harness_options: [],
    models: [],
    efforts: [],
  },
} as unknown as Fleet;
const otter = {
  session: "calm-otter",
  name: "Calm Otter",
  first_launched: "2026-01-02T03:04:05Z",
  status: "working",
} as unknown as Ship;
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status });
const PR = "https://github.com/acme/api/pull/7";

// Each test gets its own ids: a button's pending key outlives its test.
let seq = 0;
let n: string;
let cardId: string;
let reads: number;
let posts: { url: string; body: unknown }[];
let answer: () => Response;
let served: () => unknown[];

const need = (
  kind: string,
  over: Record<string, unknown> = {},
  text = "Needs you",
): {
  id: string;
  kind: string;
  text: string;
  at: string;
  card: Record<string, unknown>;
} => ({
  id: cardId,
  kind: "need_card",
  text,
  at: "2026-01-02T03:04:05Z",
  card: { item: `item-${n}`, item_kind: kind, state: "open", ...over },
});
const shipCard = (
  kind: string,
  over: Record<string, unknown> = {},
  text?: string,
) => need(kind, { ship: "calm-otter", ...over }, text);
const joined = (over: Record<string, unknown>): InboxItem =>
  ({
    id: `item-${n}`,
    session: "calm-otter",
    name: "Calm Otter",
    title: "Needs you",
    version: 1,
    created: "2026-01-02T03:04:05Z",
    ...over,
  }) as unknown as InboxItem;
const fleetItem = (over: Record<string, unknown>) =>
  joined({ session: "_admiral", name: "Admiral", ...over });

const reviewCard = () =>
  shipCard("review", {
    events: ["COMMENT", "APPROVE", "REQUEST_CHANGES"],
    discard: true,
    pr_url: PR,
  });
const reviewItem = () =>
  joined({
    kind: "review",
    review: { summary: "Two **risky** changes", review_id: 77, pr_url: PR },
  });
const finding = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  source: "reviewer",
  quote: `Quote of ${id}`,
  verdict: "agree",
  reason: `Reason for ${id}`,
  proposal: null,
  evidence: [],
  ...over,
});
const findingsCard = () =>
  shipCard("findings", {
    cards: [
      { id: "f1", proposal: null },
      { id: "f2", proposal: { kind: "diff", content: "old line" } },
    ],
  });
const findingsItem = () =>
  joined({
    kind: "findings",
    findings: {
      pr_url: PR,
      cards: [
        finding("f1"),
        finding("f2", { proposal: { kind: "diff", content: "old line" } }),
      ],
    },
  });
const routineCard = () =>
  need("routine_proposal", {
    action: "Run the nightly sweep",
    reason: "Fresh commits landed",
    expires_at: "2099-01-02T03:04:05Z",
    routine_name: "sweep",
  });
const rule = (id: string, pattern: string) => ({
  id,
  harness: "claude",
  repo: "acme/api",
  pattern: pattern.split(" "),
  every_repo: false,
  count: 1,
  reason: `Because ${id}`,
  prompts: [],
});
const rulesCard = () =>
  need("allow_rules", {
    cards: [rule("r1", "make test"), rule("r2", "go vet")],
  });
const draftCard = () =>
  need("campaign_draft", {
    repo: "acme/widgets",
    title: "Speed up the map",
    body: "Load it in under a second.",
  });

beforeEach(() => {
  n = String(++seq);
  cardId = `need-${n}`;
  reads = 0;
  posts = [];
  answer = () => json({ answered: `item-${n}` });
  served = () => [];
  fleets.value = [alpha];
  ships.value = [otter];
  inboxItems.value = [];
  loc.value = { path: "/app/chat", search: "?fleet=alpha" };
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    const path = new URL(url, "http://admiral.test").pathname;
    if (init?.method === "POST" && path.includes("/need-cards/")) {
      posts.push({ url: path, body: JSON.parse(String(init.body)) });
      return answer();
    }
    if (path.endsWith("/session")) {
      reads++;
      return json({
        fleet: "alpha",
        running: true,
        state: "idle",
        questions: [],
        messages: served(),
      });
    }
    if (path.endsWith("/fleets/alpha/repos"))
      return json({
        repos: [
          {
            path: "/w/widgets",
            name: "widgets",
            kind: "checkout",
            remotes: ["github.com/acme/widgets"],
          },
        ],
      });
    if (path.endsWith("/sources"))
      return json([
        {
          key: "github",
          plugin: "github",
          fleet: "alpha",
          name: "github",
          icon_url: "",
          has_schema: true,
          state: "ok",
          error: null,
        },
      ]);
    if (/\/sources\/github\/settings$/.test(path))
      return json({
        name: "github",
        settings: { account: "octo", host: "github.com" },
      });
    return json({});
  });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  inboxItems.value = [];
  ships.value = [];
  fleets.value = [];
});

const route = () => `/api/v1/chat/alpha/need-cards/${cardId}/answer`;
const shown = async (...entries: unknown[]) => {
  served = () => entries;
  render(<ChatPanel surface={surface} title phone />);
  await waitFor(() =>
    expect(document.querySelector(".chat-card")).not.toBeNull(),
  );
  return document.querySelector(".chat-card") as HTMLElement;
};
const button = (name: string) =>
  within(document.querySelector(".chat-card") as HTMLElement).getByRole(
    "button",
    { name },
  ) as HTMLButtonElement;
const sent = async (count = 1) => {
  await waitFor(() => expect(posts).toHaveLength(count));
  return posts[count - 1];
};

// 1. the open card shows its choices

test("an open review card shows its summary from the Inbox item, Submit with its events, and Discard", async () => {
  inboxItems.value = [reviewItem()];
  const el = await shown(reviewCard());
  expect(el.querySelector("strong")?.textContent).toBe("risky");
  expect(button("Discard")).not.toBeNull();
  fireEvent.click(button("Submit"));
  for (const ev of ["COMMENT", "APPROVE", "REQUEST_CHANGES"])
    expect(screen.getByRole("button", { name: new RegExp(ev) })).not.toBeNull();
});

test("a card for a set of finding cards shows each quote, reason and decisions, and holds Send until all are decided", async () => {
  inboxItems.value = [findingsItem()];
  const el = await shown(findingsCard());
  for (const id of ["f1", "f2"]) {
    expect(el.textContent).toContain(`Quote of ${id}`);
    expect(el.textContent).toContain(`Reason for ${id}`);
  }
  const one = within(screen.getByRole("region", { name: "Finding card 1" }));
  const two = within(screen.getByRole("region", { name: "Finding card 2" }));
  expect(one.queryByRole("button", { name: "Edit" })).toBeNull();
  for (const name of ["Approve", "Reject"])
    expect(one.getByRole("button", { name })).not.toBeNull();
  expect(two.getByRole("button", { name: "Edit" })).not.toBeNull();
  expect(button("Send").disabled).toBe(true);
  fireEvent.click(one.getByRole("button", { name: "Approve" }));
  expect(button("Send").disabled).toBe(true);
  fireEvent.click(two.getByRole("button", { name: "Reject" }));
  expect(button("Send").disabled).toBe(false);
});

test("an open routine card shows its action, reason, when it closes, Launch and Dismiss", async () => {
  const el = await shown(routineCard());
  expect(el.textContent).toContain("Run the nightly sweep");
  expect(el.textContent).toContain("Fresh commits landed");
  expect(el.textContent).toContain("Open until");
  expect(button("Launch")).not.toBeNull();
  expect(button("Dismiss")).not.toBeNull();
});

test("an open allow rules card shows each rule with Add or Skip, and holds Send until each has a decision", async () => {
  const el = await shown(rulesCard());
  expect(el.textContent).toContain("make test");
  expect(el.textContent).toContain("go vet");
  const group = (i: number) =>
    within(screen.getByRole("group", { name: `Decision for rule ${i}` }));
  for (const i of [1, 2])
    for (const name of ["Add", "Skip"])
      expect(group(i).getByRole("button", { name })).not.toBeNull();
  expect(button("Send").disabled).toBe(true);
  fireEvent.click(group(1).getByRole("button", { name: "Add" }));
  expect(button("Send").disabled).toBe(true);
  fireEvent.click(group(2).getByRole("button", { name: "Skip" }));
  expect(button("Send").disabled).toBe(false);
});

test("an open campaign draft card shows editable repo, title and body seeded from the card, with Create and Dismiss", async () => {
  await shown(draftCard());
  expect(
    (
      (await screen.findByRole("textbox", {
        name: "Title",
      })) as HTMLInputElement
    ).value,
  ).toBe("Speed up the map");
  expect(
    (
      screen.getByRole("textbox", {
        name: "Description",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("Load it in under a second.");
  await waitFor(() =>
    expect(
      (
        screen.getByRole("combobox", {
          name: "Repo for the parent issue",
        }) as HTMLSelectElement
      ).value,
    ).toBe("github.com/acme/widgets"),
  );
  expect(button("Create campaign")).not.toBeNull();
  expect(button("Dismiss")).not.toBeNull();
});

// 2. no matching Inbox item

test.each([
  ["review", reviewCard],
  ["findings", findingsCard],
])(
  "a %s card with no Inbox item still shows its choices and says the details are in the Inbox",
  async (_k, make) => {
    const el = await shown(make());
    expect(el.textContent).toContain("Details are in the Inbox");
    expect(button(_k === "review" ? "Discard" : "Send")).not.toBeNull();
  },
);

test("a card whose Inbox item matches does not say the details are in the Inbox", async () => {
  inboxItems.value = [reviewItem()];
  const el = await shown(reviewCard());
  expect(el.textContent).not.toContain("Details are in the Inbox");
});

test("an Inbox item of another session with the same id is not the card's item", async () => {
  inboxItems.value = [{ ...reviewItem(), session: "brisk-heron" } as InboxItem];
  const el = await shown(reviewCard());
  expect(el.textContent).toContain("Details are in the Inbox");
});

// 3. what a click sends

test("Submit, a review event and its confirmation post the event to the card route, edited false", async () => {
  inboxItems.value = [reviewItem()];
  await shown(reviewCard());
  fireEvent.click(button("Submit"));
  fireEvent.click(screen.getByRole("button", { name: /APPROVE/ }));
  fireEvent.click(button("Submit APPROVE"));
  const p = await sent();
  expect(p.url).toBe(route());
  expect(p.body).toEqual({ event: "APPROVE", edited: false });
});

test("opening the pull request files link first sends edited true", async () => {
  inboxItems.value = [reviewItem()];
  await shown(reviewCard());
  fireEvent.click(screen.getByRole("link", { name: /Edit/ }));
  fireEvent.click(button("Submit"));
  fireEvent.click(screen.getByRole("button", { name: /COMMENT/ }));
  fireEvent.click(button("Submit COMMENT"));
  expect((await sent()).body).toEqual({ event: "COMMENT", edited: true });
});

test("Discard on a review card posts discard true", async () => {
  inboxItems.value = [reviewItem()];
  await shown(reviewCard());
  fireEvent.click(button("Discard"));
  fireEvent.click(button("Discard review"));
  const p = await sent();
  expect(p.url).toBe(route());
  expect(p.body).toEqual({ discard: true });
});

test("Send on a set of finding cards posts every card with its decision and an edited proposal", async () => {
  inboxItems.value = [findingsItem()];
  await shown(findingsCard());
  fireEvent.click(
    within(screen.getByRole("region", { name: "Finding card 1" })).getByRole(
      "button",
      { name: "Reject" },
    ),
  );
  fireEvent.click(
    within(screen.getByRole("region", { name: "Finding card 2" })).getByRole(
      "button",
      { name: "Edit" },
    ),
  );
  fireEvent.input(screen.getByLabelText("Edit the proposal for card 2"), {
    target: { value: "new line" },
  });
  fireEvent.click(button("Save"));
  fireEvent.click(button("Send"));
  const p = await sent();
  expect(p.url).toBe(route());
  expect(p.body).toEqual({
    cards: [
      { id: "f1", decision: "rejected" },
      {
        id: "f2",
        decision: "edited",
        proposal: { kind: "diff", content: "new line" },
      },
    ],
  });
});

test("Launch on a routine card posts launch true", async () => {
  await shown(routineCard());
  fireEvent.click(button("Launch"));
  const p = await sent();
  expect(p.url).toBe(route());
  expect(p.body).toEqual({ launch: true });
});

test("Dismiss on a routine card posts dismiss true", async () => {
  await shown(routineCard());
  fireEvent.click(button("Dismiss"));
  expect((await sent()).body).toEqual({ dismiss: true });
});

test("Send on an allow rules card posts the decision of each rule by id", async () => {
  await shown(rulesCard());
  fireEvent.click(
    within(
      screen.getByRole("group", { name: "Decision for rule 1" }),
    ).getByRole("button", { name: "Add" }),
  );
  fireEvent.click(
    within(
      screen.getByRole("group", { name: "Decision for rule 2" }),
    ).getByRole("button", { name: "Skip" }),
  );
  fireEvent.click(button("Send"));
  const p = await sent();
  expect(p.url).toBe(route());
  expect(p.body).toEqual({
    cards: { r1: { decision: "add" }, r2: { decision: "skip" } },
  });
});

test("Create on a campaign draft card posts the edited repo, title and body and does not navigate", async () => {
  answer = () =>
    json({
      campaign: {
        id: "camp-9",
        url: "https://github.com/acme/widgets/issues/99",
      },
    });
  await shown(draftCard());
  const title = (await screen.findByRole("textbox", {
    name: "Title",
  })) as HTMLInputElement;
  await waitFor(() =>
    expect(
      (
        screen.getByRole("combobox", {
          name: "Repo for the parent issue",
        }) as HTMLSelectElement
      ).value,
    ).toBe("github.com/acme/widgets"),
  );
  fireEvent.input(title, { target: { value: "Speed up the fleet map" } });
  fireEvent.input(screen.getByRole("textbox", { name: "Description" }), {
    target: { value: "Under a second." },
  });
  fireEvent.click(button("Create campaign"));
  const p = await sent();
  expect(p.url).toBe(route());
  expect(p.body).toEqual({
    repo: "acme/widgets",
    title: "Speed up the fleet map",
    body: "Under a second.",
  });
  expect(loc.value.path).toBe("/app/chat");
});

test("Dismiss on a campaign draft card posts dismiss true", async () => {
  await shown(draftCard());
  await screen.findByRole("textbox", { name: "Title" });
  fireEvent.click(button("Dismiss"));
  expect((await sent()).body).toEqual({ dismiss: true });
});

// 4. collapsed states

const STATES: [Record<string, unknown>, string][] = [
  [{ state: "answered", answered_in: "chat" }, "Answered in the chat"],
  [{ state: "answered", answered_in: "inbox" }, "Answered in the Inbox"],
  [{ state: "removed" }, "Removed"],
  [{ state: "moved_on" }, "Moved on"],
];

test.each(STATES.slice(0, 3))(
  "a ship's review card served %j collapses to one line that names the ship and says %s",
  async (over, words) => {
    const el = await shown({
      ...reviewCard(),
      card: { ...reviewCard().card, ...over },
    });
    expect(el.classList.contains("quiet")).toBe(true);
    expect(el.textContent).toContain(`Calm Otter · ${words}`);
    expect(screen.queryByRole("button", { name: "Discard" })).toBeNull();
  },
);

test.each(STATES)(
  "a fleet routine card served %j collapses to one line with no ship that says %s",
  async (over, words) => {
    const el = await shown({
      ...routineCard(),
      card: { ...routineCard().card, ...over },
    });
    expect(el.classList.contains("quiet")).toBe(true);
    expect(el.querySelector(".line")!.textContent!.trim()).toBe(words);
    expect(screen.queryByRole("button", { name: "Launch" })).toBeNull();
  },
);

test.each([
  ["routine_proposal", routineCard, "Routine proposal"],
  ["allow_rules", rulesCard, "Allow rules"],
  ["campaign_draft", draftCard, "Campaign draft"],
])(
  "an open %s card names its kind in its eyebrow and no ship",
  async (_k, make, eyebrow) => {
    const el = await shown(make());
    expect(el.querySelector(".chat-card-eyebrow")!.textContent).toBe(eyebrow);
  },
);

test.each([
  ["allow_rules", rulesCard],
  ["campaign_draft", draftCard],
])("a removed %s card collapses to one line with no ship", async (_k, make) => {
  const el = await shown({
    ...make(),
    card: { ...make().card, state: "removed" },
  });
  expect(el.classList.contains("quiet")).toBe(true);
  expect(el.querySelector(".line")!.textContent!.trim()).toBe("Removed");
});

// 5. errors

test("a 409 rereads, and a card still open then shows the server's message", async () => {
  answer = () =>
    json({ error: "another action on item-1 is still running" }, 409);
  await shown(routineCard());
  const before = reads;
  fireEvent.click(button("Launch"));
  await sent();
  await waitFor(() => expect(reads).toBeGreaterThan(before));
  await waitFor(() =>
    expect(screen.getByRole("alert").textContent).toContain(
      "another action on item-1 is still running",
    ),
  );
  expect(button("Launch")).not.toBeNull();
});

test("a 409 whose reread shows the card answered shows no message", async () => {
  let entry = routineCard();
  answer = () => {
    entry = {
      ...entry,
      card: { ...entry.card, state: "answered", answered_in: "inbox" },
    };
    return json({ error: "item item-1 is already answered" }, 409);
  };
  await shown(entry);
  served = () => [entry];
  fireEvent.click(button("Launch"));
  await waitFor(() =>
    expect(document.querySelector(".chat-card.quiet")?.textContent).toContain(
      "Answered in the Inbox",
    ),
  );
  expect(screen.queryByRole("alert")).toBeNull();
});

test("an allow rules 400 saying a card has no decision rereads and shows no message", async () => {
  answer = () => json({ error: "card r3 has no decision" }, 400);
  await shown(rulesCard());
  fireEvent.click(
    within(
      screen.getByRole("group", { name: "Decision for rule 1" }),
    ).getByRole("button", { name: "Add" }),
  );
  fireEvent.click(
    within(
      screen.getByRole("group", { name: "Decision for rule 2" }),
    ).getByRole("button", { name: "Skip" }),
  );
  const before = reads;
  fireEvent.click(button("Send"));
  await sent();
  await waitFor(() => expect(reads).toBeGreaterThan(before));
  expect(screen.queryByRole("alert")).toBeNull();
  expect(document.querySelector(".chat-card")!.textContent).not.toContain(
    "has no decision",
  );
});

test.each([
  ["routine launch", 410, "the proposal expired", routineCard, "Launch"],
  ["review discard", 502, "gh could not reach GitHub", reviewCard, "Discard"],
])(
  "a %s failure of %s shows the server's message and keeps the card open",
  async (_k, status, message, make, label) => {
    inboxItems.value = [reviewItem()];
    answer = () => json({ error: message }, status);
    await shown(make());
    fireEvent.click(button(label));
    if (label === "Discard") fireEvent.click(button("Discard review"));
    await waitFor(() =>
      expect(screen.getByRole("alert").textContent).toContain(message),
    );
    expect(document.querySelector(".chat-card.quiet")).toBeNull();
  },
);

test("a campaign create that filed its parent but failed shows the error, and the reread shows the card answered", async () => {
  let entry = draftCard();
  answer = () => {
    entry = {
      ...entry,
      card: { ...entry.card, state: "answered", answered_in: "chat" },
    };
    return json(
      {
        error: "could not follow the campaign",
        parent: "https://github.com/acme/widgets/issues/98",
        answered: true,
      },
      502,
    );
  };
  await shown(entry);
  served = () => [entry];
  await screen.findByRole("textbox", { name: "Title" });
  await waitFor(() =>
    expect(
      (
        screen.getByRole("combobox", {
          name: "Repo for the parent issue",
        }) as HTMLSelectElement
      ).value,
    ).toBe("github.com/acme/widgets"),
  );
  fireEvent.click(button("Create campaign"));
  await waitFor(() =>
    expect(document.querySelector(".chat-card.quiet")?.textContent).toContain(
      "Answered in the chat",
    ),
  );
});
