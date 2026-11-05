# QQ self-addressed email login integration check

On 2026-11-05 the configured QQ SMTP account accepted a self-addressed test
message sent by Nerae's production SMTP transport. The local ignored email
configuration was read in memory; its contents and the test code were not
printed or copied into this repository.

An isolated copy of the existing email-login HTTP integration fixture then used
the same production mail transport and the self address for one challenge.
Fresh socket-only MySQL and Redis services, test key epochs and a test device
were used. The actual challenge handler enqueued the message, the outbox worker
reported QQ acceptance, the fixture derived that challenge's code in memory,
and the login and authenticated /me handlers succeeded. The targeted Go test
exited 0; the harness shut down and removed its successful private database.

The initial harness attempt stopped before sending because a Redis readiness
probe timed out. A second isolated attempt retried readiness within the
existing deadline and passed. The first failed temporary directory was retained.

This verifies QQ SMTP acceptance and the server-side login chain in one local
test. It does not prove inbox delivery or manual code entry, deployed service
configuration, actual iOS device identity, a signed App, or cross-device sync.
