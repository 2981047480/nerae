# Training entry candidate

`TrainingEntry.vue` connects the existing protected-draft factory and recording
editor for local development. A signed-in adult can discover and reopen the
current subject's drafts, create a draft, add a custom weight/repetition
exercise, switch between after-workout and per-set entry, and retry an
unconfirmed local write. The authenticated App entry now mounts the component.
Local draft status is explicitly separate from server sync; the server-save
action remains disabled. This is not a deliverable or a claim of synchronized
records.

Rapid edits are applied to the controller before queued persistence completes;
later input is not discarded merely because a write is in progress. The editor
blocks its own close action when the current draft has a pending or failed
write. A returned controller from a create/open operation is closed if its
session has changed or the component has unmounted. Discovery errors do not
present an empty list; pagination preserves earlier results and unclassified
items produce a device-level warning. Existing drafts use their frozen time
zone for date limits. The date picker asks the entry to refresh its limit on
touch, without introducing a training or rest timer.

Verification in this task worktree:

- `TrainingEntry.test.mjs`: 11 Vue component tests passed. They compile the
  actual component and use a real editor controller with synthetic storage and
  child/native boundaries. Coverage includes both modes, rapid edits, save
  failure/retry, account change and unmount during create, discovery failure,
  pagination, frozen time zone, a simulated midnight transition and the
  recording editor's picker-touch event binding and exercise selection/key
  dispatch.
- The combined 357-test training/authentication/entry suite and
  `vue-tsc --noEmit -p tsconfig.json` passed.
  `scripts/quality/verify_uts_plugin_build.py` was extended to import this
  component and built both App and Weixin probes with compiler 5.33. The actual
  App, Weixin and H5 resource builds also passed when invoking the installed
  compiler directly. `pnpm run build` itself stopped before building because
  pnpm wanted to reinstall the shared, symlinked dependencies without an
  interactive terminal. The direct compiler checks do not establish native
  execution.
- Read-only review identified the initial midnight and navigation gaps; the
  component now refreshes the date on picker touch and exposes `canLeave()`;
  the App root consults it for its close and system-back paths. The review did
  not establish real-device acceptance.
- A disposable local WebKit fixture rendered the actual recording editor at
  375 x 812 with synthetic exercises and a replicated toolbar. Both modes
  were inspected and a DOM check found no horizontal overflow for the input,
  confirmation and mode controls. This is an internal layout check, not an
  installed App, and does not verify the native keyboard, picker or gestures.

Remaining boundaries: the actual native system-back event has not been tested;
forced process termination cannot be intercepted and may lose the latest
failed in-memory write. Actual iPhone picker touch/update ordering, layout
and accessibility remain unverified. The component supports custom
weight/repetition exercises only;
Android and Weixin protected draft ports remain unavailable. Real account
login, server-backed save, receipt settlement, cross-device recovery and
installation remain unfinished.
