# stream

Fans small "something changed" messages out to connected consoles over server-sent events (`GET /api/stream`).

- Messages carry no notification content (`ready`, `event` with id and class, `inbox`); they only cue a refresh, so what a person may see is still decided by the API.
- `Broker.Publish` never blocks: a client with a full buffer loses its oldest message. A cap (100 connections) protects the process.
- The server ends every stream on shutdown, extends each write's deadline itself (the normal write timeout would cut a stream) and sends a heartbeat every 25 s.
- The console reads the stream with `fetch` so the API key travels in a header, never in the URL.
