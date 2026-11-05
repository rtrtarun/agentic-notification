# channels

Describes the configured delivery channels (email, Slack, Teams, SMS) and sends a test message through one.

- `Describe(Settings)` returns each channel with a **masked** target: email keeps the first letter and domain, webhook URLs show only their host because the path is the credential.
- Each channel reports which attention classes it `receives`, from the route table (`NOTIFY_STANDARD_CHANNELS`, `NOTIFY_BACKGROUND_CHANNELS`); critical is always included. The SMS channel shows `mode: "mock"` when its gateway only logs.
- `Tester.Test(ctx, id)` sends one message labelled `[TEST]` through that channel only, and refuses a second test of the same channel inside the minimum gap (10 s in the server) so the button cannot flood a channel.
- Errors: `ErrUnknown` (404), `ErrNotConfigured` (409), `ErrTooSoon` (429). A delivery failure is logged and answered generically (502) because the error can name hosts or addresses.
