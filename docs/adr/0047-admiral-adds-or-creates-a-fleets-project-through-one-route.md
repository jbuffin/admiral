# 47. Admiral adds or creates a fleet's project through one route

Status: accepted

## Context

Admiral asks for a repo in two places: the launch dialog's repo picker and the Add campaign modal's confirm step. Both offer only the fleet's projects, the repos checked out under its search roots. [#748](https://github.com/jbuffin/admiral/issues/748) asked that the engineer be able to name a repo that isn't one, and have Admiral add it to the fleet, or create it on GitHub when it is new. A clone on launch already exists ([ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md)'s amendment): it clones a hinted repo into the fleet's clone root, but it can't tell when the repo is already another fleet's project. AdmiralBot is fenced to an MCP server ([ADR 0023](0023-admiralbot-is-a-coordinator-fenced-to-an-admiral-mcp-server.md)) and gets work done by launching ships ([ADR 0041](0041-admiralbot-sends-scouts-quick-ships-and-commodore-ships.md)). Every ship needs a checkout to run in, and a no-skill ship's worktree branches from the remote's default branch ([ADR 0043](0043-an-idea-in-the-chat-becomes-a-pr-with-no-questions-on-the-way.md)), which a repo that doesn't exist yet doesn't have.

## Decision

**Adding a project means Admiral clones the repo into the fleet's clone root, and creating one means Admiral creates it on GitHub and then adds it. Both go through one server route that enforces the rules below. AdmiralBot never calls it: it proposes a new repo on a card, and the engineer's tap runs it.**

- **Add.** The repo goes to `<clone_root>/<repo>`, as a clone on launch does, and a folder already there is refused. Admiral acts as the fleet's GitHub source account for the repo's host; a fleet with no source for that host can't add from it.
- **A project of at most one fleet.** Every clone, including a clone on launch, is refused when the repo is already a project of another fleet, and the refusal names that fleet.
- **Create only on the engineer's word.** Admiral looks the name up first. Only a name that doesn't exist offers Create, behind its own confirm that shows the owner, visibility and target path.
- **Who may own a new repo.** Only the fleet account's own user or one of the fleet's Owners.
- **What a new repo starts as.** Private unless the engineer picks Public, with a README and an optional one-line description, so it has a default branch for the first ship's worktree.
- **The add runs on its own.** Choosing Add or Create runs it at once, and the repo is then selected like any project. A campaign create or launch that follows is a separate request.
- **Admiral never deletes a repo.** When a create succeeds and the clone fails, the repo stays on GitHub and the entry becomes Add. When the add succeeds and the next step fails, the repo stays a project.
- **AdmiralBot proposes, through a card.** `propose_new_repo` posts a card with the owner, name, visibility, description and the ship's goal. The engineer's **Create and launch** runs the route and then launches the ship in a worktree of the new project, which opens a PR as usual. A change of name or owner is a chat reply and a new card. When the create succeeded but the launch failed, the card offers **Launch again**. AdmiralBot is not told the outcome.

## Considered options

- **Admiral creates the repo on the engineer's tap** (chosen). The owner limit, visibility and README are enforced in code, in one place.
- **The ship creates it.** A ship launched in the clone root runs `gh repo create`. The owner limit would be a prompt rule on an unattended ship, which could create repos under any org the account can write to, and its first push to an empty default branch has no PR to moor on.
- **The add happens on submit**, inside the campaign create or the launch. One request, but a clone of up to ten minutes inside a create, and two kinds of failure in one error.
- **Any org the account can create in.** Owners decide routing, but creating writes to someone's org, so it stays with the accounts the fleet already claims.

## Consequences

- Amends [ADR 0009](0009-ships-run-in-a-checkout-picked-from-the-fleets-search-roots.md): a clone on launch is refused for a repo that is another fleet's project.
- Amends [ADR 0041](0041-admiralbot-sends-scouts-quick-ships-and-commodore-ships.md): a ship that starts a new repo is proposed on a card, not launched by AdmiralBot.
- A repo created by mistake has to be deleted by hand on GitHub.
- The fleet's Preferences page gets no Add project button.
