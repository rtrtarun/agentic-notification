# Milestone 6: Delivery Layer Abstraction

## Architecture

- Keep delivery provider-specific logic out of the core service layer.
- Introduce a small provider interface that can later be implemented by Novu, email, Slack, or other channels.
- Keep the first implementation as a stub so the boundary is established without introducing external dependencies.

## Tasks

- Add a delivery provider interface.
- Add a delivery service that routes requests through the configured provider.
- Keep the server and notification flow provider-agnostic.

## Implementation

- The delivery package now exposes a Provider interface and a simple Service wrapper.
- The current provider implementation is a stub, which makes the architecture ready for real providers later.

## Tests

- Verify the delivery service uses the configured provider.

## Review checklist

- [x] Delivery abstraction exists.
- [x] Provider interface is isolated from core logic.
- [x] Backend tests pass.
