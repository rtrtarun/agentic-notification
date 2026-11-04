# Attention console

React and TypeScript operator console for the Attention Orchestration Platform. It shows what the system decided about each event and why, and sends feedback.

```bash
npm install
npm run dev      # http://localhost:3000
npm test         # unit tests for src/lib
npm run build    # type-check and production build
```

- `VITE_BACKEND_URL`: service address (default `http://localhost:8080`).
- `VITE_API_KEY`: **local development only.** Anything in a built bundle is public. Normally you enter the key in Connection settings; it is kept in this tab's `sessionStorage`.
- The service must allow this origin: `CORS_ALLOWED_ORIGINS=http://localhost:3000`.

Design language, states, copy rules and structure: [docs/frontend.md](../../docs/frontend.md).
