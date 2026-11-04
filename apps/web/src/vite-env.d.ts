/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_BACKEND_URL?: string
  // Local development only. A key in a built bundle is public.
  readonly VITE_API_KEY?: string
  // 'true' makes a first visit open in demo mode (used by the hosted build).
  readonly VITE_DEFAULT_DEMO?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
