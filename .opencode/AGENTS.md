# Project-Level Kanban & Planning Rules

## Session Start Protocol

Every session begins by reading these files for context:

1. Read `KANBAN.md` — current board state (Backlog, Known Bugs, To Do, In Progress, Done)
2. Read `PLAN.md` — architecture plan, phased delivery checkboxes, coverage targets
3. Initialize `todowrite` with items from KANBAN.md's **In Progress** section (as `in_progress`) and **To Do** section (as `pending`), plus any **Known Bugs** marked as active.
4. Report to the user: "Loaded context from KANBAN.md / PLAN.md — [N] items in progress, [N] pending, [N] known bugs."

## Automatic Kanban Updates

The agent MUST keep KANBAN.md and PLAN.md synchronized with actual work automatically — do NOT wait for the user to prompt this.

### When an item moves in `todowrite`

| todowrite transition | KANBAN.md action |
|---|---|
| `pending` → `in_progress` | Move item from **To Do** (or **Known Bugs** / **Backlog**) to **In Progress** |
| `in_progress` → `completed` | Move item from **In Progress** to **Done** with brief summary of what was done |
| `in_progress` → `cancelled` | Move item from **In Progress** back to **To Do** or **Backlog** with note |

### After any substantial change (new files, new endpoints, new structs, new tests)

Update **PLAN.md** if:
- A new package or module is added → update directory layout
- A phased delivery checkbox changes state
- A coverage target is met
- API endpoints or data models change

Update **KANBAN.md** if:
- A bug is discovered during work → add to **Known Bugs**
- A feature is scoped → add to **To Do** or **Backlog**
- A test is added → update the test count in **Done** items
- Session summary changes → update `*Last updated*` date

### End-of-session checklist

Before the session concludes, verify:

1. `KANBAN.md` `*Last updated*` date is current
2. All `todowrite` items that are `completed` appear in **Done**
3. All `todowrite` items still `in_progress` or `pending` appear in **In Progress** or **To Do**
4. Any newly discovered bugs appear in **Known Bugs**
5. **PLAN.md** phased delivery checkboxes match reality
6. `go build ./...` and `go test ./... -race -count=1` still pass
