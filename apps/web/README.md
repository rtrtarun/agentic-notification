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

Open the console with no service running and choose **Try the demo** to explore it with sample data. The GitHub Pages build starts in demo mode; see [docs/deployment.md](../../docs/deployment.md).

Design language, states, copy rules and structure: [docs/frontend.md](../../docs/frontend.md).

## Screens

| Screen | Route | Needs |
|---|---|---|
| Overview | `#/overview` | `/api/analytics`, `/api/notifications` |
| Inbox | `#/inbox` | `/api/notifications` |
| All Notifications | `#/all` | `/api/notifications` |
| Activity Timeline | `#/activity` | `/api/activity` |
| Rules and Workflows | `#/rules` | `/api/rules` |
| Priority Policies | `#/policies` | `/api/policies` |
| Notification Channels | `#/channels` | `/api/channels` |
| Integrations | `#/integrations` | `/api/integrations` |
| Analytics | `#/analytics` | `/api/analytics` |
| Preferences | `#/preferences` | `/api/preferences`, `/api/stream` |
| Settings | `#/settings` | none |
| Help and Support | `#/help` | `/health` |

Every screen works in demo mode. Screens from milestone 16 on are English first; see the language note in [docs/milestone-16.md](../../docs/milestone-16.md).
