# Retry a failed local training-draft write

`TrainingEditorController.retryLocalSave()` retries the current draft snapshot
without changing the entered data or claiming a server save. It uses the
existing per-draft write queue and session lease. A retry still reports failure
when device storage fails, another edit supersedes the snapshot, or the account
changes while the write is pending. A write that returned an unknown outcome
is retried with the current local snapshot; this is not a network submission.

Four focused tests cover failed storage followed by retry, an applied write
whose result is lost, a newer queued edit, and a session change during retry.
The first two original tests failed before the method existed. All 221 training
tests and client `vue-tsc --noEmit -p tsconfig.json` passed in the task worktree.
Independent read-only review found no reproducible incorrect-success or data
loss path in the stated in-process scope and requested the lost-result test,
which was added and passed.

The input may remain only in memory until a retry is confirmed; app termination
before that point can still lose the latest failed write. This method has no
page caller yet and is not server synchronization or iPhone evidence. The
separate draft-entry component in this worktree remains unreviewed and is not
part of this change.
