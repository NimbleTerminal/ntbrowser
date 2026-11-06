# Offline compatibility fixtures

Run `GOWORK=off CGO_ENABLED=0 go test ./loop -run TestCompatibilityFixtures`.
The ordinary test suite and `task ci` include this test.

These small authored pages reproduce API combinations encountered while
diagnosing real sites, without depending on Google, CAPTCHA, or network access.

- `start.html`: cookies and session storage, lexical direct eval, a Promise
  microtask before a timer, timer/frame cancellation, performance clocks, and
  a Location replacement requested from an animation callback.
- `next.html`: fresh script realm with retained same-origin cookie/storage
  state, and an animation mutation that must reach the rendered frame.
- `isolated.html`: another host cannot read the first host's state; its writes
  must not overwrite that state when the first page is revisited.
- `closed.html`: the default profile blocks cookies, storage, navigation, and
  dynamic compilation while ordinary timers and animation callbacks still work.

The Go harness supplies HTTPS document URLs and a memory-only cookie store.
It checks the navigation message's source, generation, destination and mode,
then supplies the destination HTML itself. This tests the VM/loop boundary;
it does not exercise HTTP redirects or the application's navigation handling.
Uncaught script errors fail immediately; completion markers and rendered text
have bounded waits. The suite asserts causal ordering, not elapsed milliseconds
or an ordering between independent timers and animation frames.

Add focused pages here when another compatibility failure is fixed. Keep
network resources and site-specific response bodies out of these fixtures.
