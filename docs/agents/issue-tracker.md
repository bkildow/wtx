# Issue tracker: beads (`br`)

Issues and specs for this repo live in beads, managed with the `br` CLI. `.beads/` is gitignored, so the backlog is local to this machine: never reference bead IDs from committed files, commits or PR bodies as if others can resolve them.

## Conventions

- **Create an issue**: `br create --title "..." --type <task|bug|feature|epic|chore|docs> --priority <0-4> --description "..."`. For multi-line bodies write to a temp file and pass `--description "$(cat file)"`. Add `--silent` to get just the ID back.
- **Spec**: an issue of `--type epic` whose description holds the spec. Tickets carved from it take `--parent <epic-id>`.
- **Read an issue**: `br show <id>` (add `--json` for machine-readable), and `br comments <id>` for the conversation.
- **List issues**: `br list --status=open` with `--json`; `br search "<text>"` for full-text.
- **Comment**: `br comments add <id> "..."` (or `--file <path>` for long text).
- **Labels**: `br label add <id> <label>` / `br label remove <id> <label>`.
- **Blocking edges**: `br dep add <blocked-id> <blocker-id>` (default type `blocks`). `br blocked` lists blocked issues; `br dep tree <id>` shows the graph.
- **Claim**: `br update <id> --claim` (or `--status=in_progress`).
- **Close**: `br close <id> --reason "..."`.
- After writes, run `br sync --flush-only` to export to JSONL.

## When a skill says "publish to the issue tracker"

Create a bead with `br create`.

## When a skill says "fetch the relevant ticket"

Run `br show <id>` and `br comments <id>`.

## Wayfinding operations

Used by `/wayfinder`. The **map** is an epic with **child** beads as tickets.

- **Map**: `br create --type epic --labels wayfinder:map`, holding the Notes / Decisions-so-far / Fog body in its description.
- **Child ticket**: `br create --parent <map-id> --labels wayfinder:<research|prototype|grilling|task>`.
- **Blocking**: `br dep add <child> <blocker>`. A ticket is unblocked when every blocker is closed.
- **Frontier query**: `br ready --json`, filtered to children of the map that are unassigned; lowest ID wins.
- **Claim**: `br update <id> --claim`, the session's first write.
- **Resolve**: `br comments add <id> "<answer>"`, `br close <id>`, then append a context pointer (gist + bead ID) to the map's Decisions-so-far via `br update <map-id> --description`.
