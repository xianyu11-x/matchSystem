/// <reference types="vite/client" />

interface Window {
  __MATCH_DESKTOP__?: boolean
  /** Optional Tauri sidecar/runtime injection point. Web deployments use VITE_API_BASE_URL. */
  __MATCH_API_BASE_URL__?: string
}
