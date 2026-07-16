---
id: K-075
# Filename pattern: {ID}-{slugified-title}.md
title: HMS dismiss/action flow
initiative_id: null             # set to an initiatives/<id> slug if part of a cross-repo initiative
claimed_by: clever-fennec-reef                 # pet name of the agent session working this card, e.g. otter
claimed_at: 2026-07-16T03:15Z                 # ISO8601, paired with claimed_by
blocks: null                     # set on a child/sub-blocker card: the parent card id it blocks
blocked_by: null                     # set on a card that can't proceed until another card finishes
related_cards: []
---

# K-075 — HMS dismiss/action flow

## Context

HMS dismiss/action flow. Bigger feature — needs exploration and planning. Handles dismissable HMS errors, action UI for user-initiated fixes. Needs Explorer/Planner to scope first.

## Plan
<!-- ordered checklist. Prefix steps with the role expected to do them once a card
     has been planned out, e.g. "Implementer: apply config change". -->
1. [x] Planner: Design HMS dismiss/action architecture (data model, API, UI flow)
2. [x] Implementer: Add `internal/server/hmsdismiss.go` (`HMSDismissTracker`: `Dismiss`/`IsDismissed`/`Reconcile`, mirroring `skiptracker.go`'s shape). Wire `hmsDismissTracker *HMSDismissTracker` into `Server` + `newTestServer`; call `Reconcile` from the same status-update hook `clearSkippedOnPrintEnd` already uses in `startStatusForwarder`, using that update's HMSErrors+HMSWarnings codes as the active set. Commit this step alone before moving on.
3. [x] Implementer: Add `POST /api/printers/{id}/hms/dismiss` route + `handleDismissHMS` (body `{"code":"..."}`, 400 on bad/missing body — mirror `handleSetBedTemp`'s required-body pattern — 404 via existing `getPrinter`, then `Dismiss` + `{"status":"ok"}`). Filter dismissed codes out of `HMSErrors`/`HMSWarnings` at every point status is serialized outward (`handleListPrinters`, `handleGetPrinter`, WS broadcast) — do NOT touch `PrinterStatus`, the `Printer` interface, or any driver (`bambu/client.go`) code; dismissal is server/display-layer only. Commit alone.
4. [x] Implementer: Redesign HMS UI in `onboarding.go` — replace the `warningHtml`/`hmsSummary()`-joined string with a per-entry row list (iterate filtered `hms_errors` + `hms_warnings`, one row each: severity styling, message-or-code text, Dismiss button) in both `renderCard()` and `updateCard()` (keep them in sync — see the existing K-053 comments about exactly this class of drift). Leave `.error-banner`/`error_msg` untouched for non-HMS errors (print_error/MQTT-disconnect messages have no HMS code to dismiss by). Commit alone.
5. [x] Implementer: Add JS `dismissHms(id, code)` (POST to the new endpoint, then optimistically hide that row client-side — no new `window._dismissedHMS`-style cache; trust the next poll/WS `updateCard()` to already reflect server-side filtering). Wire each row's Dismiss button. Commit alone, then run `go build ./...`, `go vet ./...`, `go test ./...`.
6. [x] Inspector: Verify dismiss flow — done via code trace + tests (no real Bambu printer available in this environment; see decision log for exactly what was/wasn't verified and the residual hardware-smoke-test follow-up).

## Signals
<!-- append-only. Leave signals for other agents. Format:
     <!-- signal: <pet-name> <ISO8601-UTC> — <short message> -->
-->
<!-- signal: clever-fennec-reef 2026-07-16T03:15Z — claiming, starting at Plan step 2 (HMSDismissTracker) -->
<!-- signal: clever-fennec-reef 2026-07-16T03:24Z — step 2 done (commit 4b3901c on worktree-clever-fennec-reef), starting step 3 (dismiss route + filtering) -->
<!-- signal: clever-fennec-reef 2026-07-16T03:40Z — step 3 done (commit 2458b30), starting step 4 (onboarding.go UI redesign) -->
<!-- signal: clever-fennec-reef 2026-07-16T03:58Z — step 4 done (commit 8ac6e21), starting step 5 (JS dismissHms wiring) -->
<!-- signal: clever-fennec-reef 2026-07-16T04:10Z — step 5 done (commit 2b555ca), all 4 implementer commits landed on worktree-clever-fennec-reef (4b3901c, 2458b30, 8ac6e21, 2b555ca); starting step 6 (inspector) — no real Bambu printer available in this environment, doing best-effort verification instead -->

## Working context

### Current HMS Implementation
- **Backend**: `HMSEntry` struct (Code, Module, Severity, Message) in `internal/printers/interface.go`
- **Severity split**: fatal/serious → `HMSErrors` (trips state="error"), common/info/unknown → `HMSWarnings`
- **Message lookup**: Vendored JSON table from HA-Bambu (`hms_messages.go`)
- **Staleness decay**: `hmsHealthyStreak` mechanism clears stale HMS when firmware stops sending

### Current UI
- Single `.error-banner` div for state="error" with `error_msg`
- Single `.warning-banner` div for HMS warnings
- Both display concatenated text via `hmsSummary()` — no per-entry interaction

### What's Missing
1. **No dismiss mechanism**: Users cannot acknowledge/dismiss HMS entries
2. **No action UI**: No buttons for user-initiated fixes (clean, calibrate, etc.)
3. **No API endpoint**: Server has no dismiss/action route
4. **No per-entry granularity**: All errors/warnings shown as single string

### Key Files
- `internal/printers/interface.go` — HMSEntry, PrinterStatus
- `internal/printers/bambu/commands.go` — command structs (needs new HMS commands)
- `internal/printers/bambu/client.go` — handleReport, HMS parsing
- `internal/server/server.go` — API routes (needs new endpoints)
- `internal/server/onboarding.go` — indexDashboardTemplate (UI)

## Design: HMS Dismiss Architecture

### Scope correction vs. the original (fabricated) plan
The original Plan steps 2–5 assumed a `DismissHMS` method on the `Printer`
interface + Bambu client, i.e. dismissal as driver-layer state. Redesigning
this as **server/display-layer only**, no interface or driver change, for the
same reason K-076's `SkipTracker` (already built, in `internal/server/`,
merged) stayed out of the driver: dismissal is "I've seen this, stop showing
it to me" — a UI acknowledgement, not a change to printer reality. Fatal/
serious HMS entries independently trip `State="error"`/`ErrorMsg` inside
`bambu/client.go`'s `handleReport` (ground truth); dismissing an entry must
NOT touch that — only whether the per-entry row still renders. The printer's
state tag (already rendered elsewhere from `state`, independent of the
banner — see K-076 decision log's mention of it) keeps signaling "this
printer has an active error" even if every HMS row under it has been
dismissed. This mirrors standard toast/alert-dismiss UX: dismiss quiets the
notification, doesn't fix or hide the underlying condition.

### Data model — `internal/server/hmsdismiss.go` (new, mirrors `skiptracker.go`)
```go
type HMSDismissTracker struct {
    mu        sync.RWMutex
    dismissed map[string]map[string]struct{} // printerID -> code -> dismissed
}

func NewHMSDismissTracker() *HMSDismissTracker
func (t *HMSDismissTracker) Dismiss(printerID, code string)
func (t *HMSDismissTracker) IsDismissed(printerID, code string) bool
func (t *HMSDismissTracker) Reconcile(printerID string, activeCodes map[string]struct{})
```
Keyed per-printer, same as `SkipTracker`. `Reconcile` drops any dismissed
code no longer present in the printer's current HMSErrors+HMSWarnings — this
answers the "does dismiss persist across a fresh re-fire?" question from the
prior handoff: **no** — dismissal only suppresses the *current* occurrence.
Once the code stops being reported (condition resolved) and later reappears,
it's a fresh occurrence and renders un-dismissed. Call `Reconcile` from the
same per-update hook `clearSkippedOnPrintEnd` already uses in
`startStatusForwarder` (K-076 precedent), so it runs once per status update
regardless of how many times that status gets serialized out afterward.

### API shape
`POST /api/printers/{id}/hms/dismiss`, body `{"code": "<hms code>"}`
(single code per call — matches per-entry dismiss buttons; entry counts are
small so no batch/all-at-once variant is needed). 400 on missing/unparsable
body, 404 via the existing `getPrinter` check, else `Dismiss` +
`{"status":"ok"}`. No separate GET-dismissed endpoint needed (unlike
`SkipTracker`'s `/skipped`, which *adds* info) — dismissal *filters* the
existing `hms_errors`/`hms_warnings` fields already sent by
`handleListPrinters`/`handleGetPrinter`/the WS broadcast, so the frontend
never needs to fetch or track a separate dismissed set.

### UI flow
Replace `warningHtml`'s current `hmsSummary()` (joins all warning entries
into one string) with a per-entry row list built from filtered
`hms_errors`+`hms_warnings`, each row: severity color, message-or-code text,
a Dismiss button (`onclick="dismissHms(id, code)"`). Applies to both
`renderCard()` and `updateCard()` — keep them in lockstep the way
`bannerHtml()`/`toggleBanner()` already do for the existing banners (repo's
own comments cite K-053 for the cost of letting these two paths drift).
Non-HMS `error_msg` (print_error / MQTT-disconnect — no HMS code to key a
dismiss on) keeps using the existing plain `.error-banner`, untouched.
`dismissHms(id, code)` POSTs to the new endpoint then optimistically hides
the row; no new client-side dismissed-state (no `window._dismissedHMS`) —
server-side filtering means the next poll/WS update already reflects it,
avoiding inventing client state that has to stay in sync with server state.

### Explicitly descoped: the "action" half of "dismiss/action flow"
No HMS-code → remedial-action mapping exists anywhere in the codebase (e.g.
"this code means clean the nozzle" / "recalibrate"). Building that mapping is
a separate, larger effort with its own research needs (what actions are even
safe to expose per code, per printer model). Scoping this card to dismiss
only; recommend a follow-up card for the action-button half once dismiss
ships and real HMS code frequency data from actual use can inform which
actions are worth building first.

## Decision log

- **Inspector verification (K-075 step 6)**: CRITICAL — handoff notes claim steps 2–5 are complete, but none of the described code exists in the codebase. Ran `rg` and `grep` across the entire repo for `DismissHMS`, `dismissedHMS`, `hms/dismiss` — zero matches. Direct read of `interface.go` confirms the Printer interface has no `DismissHMS` method. Build and tests pass trivially because nothing was changed. Steps 2–5 must be re-implemented from scratch.
- 2026-07-15 — Implementer (self-pulled per the persistent-pane protocol):
  card's `current_role` frontmatter said `implementer`, but the Plan
  checklist itself still had step 1 (Planner) unchecked, and the Inspector's
  own Handoff notes explicitly recommend "start with step 1 (planner) to
  re-confirm the architecture" before any more implementation. Since there is
  no actual design on this card to implement against (Working context is
  Explorer-level current-state notes only, not an API/data-model/UI-flow
  design), correcting `current_role` to `planner` rather than guessing at an
  implementation. Releasing claim and dispatching Planner.
- 2026-07-15 — Planner: designed the dismiss architecture (see "Design: HMS
  Dismiss Architecture" above). Key call: descoped the original Plan's
  driver-layer `DismissHMS` (Printer interface + Bambu client) in favor of a
  server/display-layer-only tracker, following the `SkipTracker` precedent
  from K-076 — dismissal is a UI acknowledgement, not a change to printer
  reality, so `State`/`ErrorMsg` computed in `bambu/client.go` must stay
  untouched. Also explicitly descoped the "action" (remedial-fix buttons)
  half of the card title — no code→action mapping exists yet; recommending a
  follow-up card once dismiss ships. Re-scoped Plan steps 2–5 against this
  design, each ending in its own commit per the card's own prior suggestion
  (and Inspector's finding that un-committed "done" steps went undetected
  last time). Setting `current_role: implementer`, releasing claim, handing
  off.
- 2026-07-16 — clever-fennec-reef (worker): implemented steps 2–5, each as
  its own real, verified commit on `worktree-clever-fennec-reef`:
  `4b3901c` (HMSDismissTracker), `2458b30` (dismiss endpoint + outbound
  filtering + tests), `8ac6e21` (per-entry dismissible-row UI), `2b555ca`
  (JS wiring). `go build`/`go vet`/`go test ./...` clean after every step,
  each verified by re-reading `git log` output before moving on — this is a
  deliberate response to the prior incident on this card where steps were
  checked off with no commits behind them.
- 2026-07-16 — clever-fennec-reef (worker), step 6 (Inspector): dispatched a
  Review pass to verify the four claims in the original step 6 ask.
  **No real Bambu printer was available in this environment**, so "verify
  with real Bambu printer" could not be performed literally. Instead, each
  claim was verified as rigorously as automation allows:
  - *Dismissed row disappears*: verified via `TestHandleListPrinters_FiltersDismissedHMS` / `TestHandleGetPrinter_FiltersDismissedHMS` (both pass) plus code reading of the JS wiring.
  - *State tag stays "error", ground truth unaffected*: verified by code trace — `State` is computed in `bambu/client.go`'s `handleReport` from the driver's own unfiltered HMS data before the status ever reaches `server.go`; `filterDismissedHMS` operates on a value-copy only at the outbound-serialization boundary, after `State` is already fixed. Dismissal never touches driver state.
  - *Survives page reload (server-side, not per-tab)*: verified — `HMSDismissTracker` lives on `Server`, in-memory, no per-session scoping; grepped all four commits for `localStorage`/`sessionStorage` — zero matches.
  - *Reconcile un-suppression (clear-then-refire not permanently suppressed)*: verified via a standalone scratch test directly exercising `HMSDismissTracker.Reconcile` (dismiss → reconcile with code absent from active set → confirmed un-dismissed → reconcile with code back in active set → confirmed still not re-suppressed). Scratch test was not committed.
  No bugs found. **Residual gap, explicitly not swept under the rug**: real
  Bambu MQTT wire behavior (whether firmware re-sends the exact same `code`
  string on re-occurrence, timing of reconcile relative to real fault
  cycles, live-browser/WS rendering) was not and could not be exercised
  here. Recommend a real-hardware smoke test at or shortly after deploy;
  filing this as a natural follow-up rather than blocking board closure on
  hardware this environment doesn't have. The originally-scoped "action"
  half of the card title (remedial-fix buttons) was already descoped by the
  Planner (see above) — a separate follow-up card if wanted.

## Handoff notes

**2026-07-16 — clever-fennec-reef: card complete, PR open.**
PR: https://github.com/chrisjohnson/printer-dashboard/pull/2 (branch
`worktree-clever-fennec-reef` → `main`). All 4 implementation commits
(`4b3901c`, `2458b30`, `8ac6e21`, `2b555ca`) plus the step-6 verification
pass are described in the Decision log above. One open item before/at
deploy: a real-hardware smoke test against a live Bambu printer (not
possible in this dev environment) — see PR description's unchecked test-plan
item. No blocking bugs found.

---

**Inspector found: implementation not present.** Steps 2–5 were marked complete in the plan but the code changes do not exist in the codebase:

- `DismissHMS` method not in `internal/printers/interface.go`
- No `dismissedHMS` tracking in `internal/printers/bambu/client.go`
- No `/api/printers/{id}/hms/dismiss` endpoint in `internal/server/server.go`
- No dismiss UI or `window._dismissedHMS` in `internal/server/onboarding.go`

Build + tests pass because nothing was changed. Implementer needs to redo steps 2–5. Suggested approach: start with step 1 (planner) to re-confirm the architecture, then implement step-by-step, committing after each step so progress is visible in git.

Next: **Implementer** — the design is done (see "Design: HMS Dismiss
Architecture" section above) and Plan steps 2–5 are re-scoped against it.
Concretely, in order:

1. `internal/server/hmsdismiss.go` (new) — `HMSDismissTracker`
   (`Dismiss`/`IsDismissed`/`Reconcile`), mirror `skiptracker.go`'s shape
   exactly (RWMutex, per-printer map, `NewXTracker()` constructor). Wire into
   `Server` struct + `newTestServer` in `server_test.go`. Call `Reconcile`
   from wherever `clearSkippedOnPrintEnd` is invoked in `startStatusForwarder`
   (same per-update hook), passing that update's HMSErrors+HMSWarnings codes
   as the active set. **Commit this step by itself.**
2. `POST /api/printers/{id}/hms/dismiss` route + `handleDismissHMS` in
   `server.go` — body `{"code":"..."}`, required (400 if missing/unparsable,
   like `handleSetBedTemp`), 404 via `getPrinter`, else `Dismiss` +
   `{"status":"ok"}`. Then filter dismissed codes out of `HMSErrors`/
   `HMSWarnings` at every outbound serialization point — `handleListPrinters`,
   `handleGetPrinter`, and the WS broadcast path. Do not touch
   `PrinterStatus`, the `Printer` interface, or `bambu/client.go`. **Commit by
   itself**, with a test mirroring `TestHandlePause`'s found/not-found/error
   subtests plus a filtering-applied-to-list-output case.
3. UI in `onboarding.go` — replace `warningHtml`/`hmsSummary()`'s joined
   string with a per-entry row list (severity, message-or-code, Dismiss
   button) built from the (now server-filtered) `hms_errors`+`hms_warnings`,
   in both `renderCard()` and `updateCard()` — keep them in lockstep like
   `bannerHtml()`/`toggleBanner()` already are (see the repo's own K-053
   comments on this exact drift risk). Leave `.error-banner`/`error_msg`
   (non-HMS errors) untouched. **Commit by itself.**
4. JS `dismissHms(id, code)` — POST to the new endpoint, optimistically hide
   the row, no new client-side dismissed-state cache. Wire each row's Dismiss
   button. **Commit by itself**, then run `go build ./...`, `go vet ./...`,
   `go test ./...` and confirm clean before handing off.

Hand to **Inspector** for plan step 6 once all four commits land and the
build/vet/test trio is clean — this repeat of the incremental-commit
discipline is deliberate, per the prior Inspector's finding that "steps
marked done" with no actual commits went undetected until a full Inspector
pass.

## Team conversation

- 2026-07-15 (planner): Attempted `fleet-spawn-teammate.sh
  /Users/chrisjohnson/src/chrisjohnson/printer-dashboard K-075 implementer`
  to hand off per section 5's protocol. Failed immediately: `$TMUX/$TMUX_PANE
  is not set` — this session is a background job, not a live tmux client
  pane, so the script can't self-identify or split a pane. This isn't a
  fixable input typo (unlike the K-076 `<repo>` path issue), so per F-047
  stopping after one look rather than retrying blindly or hand-enumerating
  tmux panes. `current_role` is already set to `implementer` and the design/
  handoff notes above are complete — card just needs a human or a
  tmux-attached session to actually run the spawn (or to `fleet-adopt-pane.sh`
  it directly). Leaving the card in `now/`, unclaimed.
