import type { ReactNode } from 'react'
export type IconName = 'family' | 'grid' | 'home' | 'chat' | 'user' | 'members' | 'plus' | 'arrow' | 'back' | 'spark' | 'lock' | 'logout' | 'clock' | 'chevron' | 'close' | 'book' | 'device' | 'sensor' | 'workflow' | 'refresh' | 'send' | 'check' | 'warning' | 'error' | 'search' | 'info'
const grid = <><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></>
const paths: Record<IconName, ReactNode> = {
  family: grid, grid,
  home: <><path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"/><path d="M9 21v-7h6v7"/></>,
  chat: <><path d="M21 11a8 8 0 0 1-8 8H8l-5 3 1-6a8 8 0 0 1-1-4 9 9 0 0 1 18-1Z"/><path d="M8 10h8M8 14h5"/></>,
  user: <><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/></>,
  members: <><circle cx="9" cy="8" r="3"/><path d="M3 20v-2a6 6 0 0 1 12 0v2M17 6a3 3 0 0 1 0 6M18 14a5 5 0 0 1 3 5v1"/></>,
  plus: <path d="M12 5v14M5 12h14"/>, arrow: <path d="M5 12h14m-6-6 6 6-6 6"/>, back: <path d="M19 12H5m6-6-6 6 6 6"/>,
  spark: <path d="m12 2 2 7 7 3-7 3-2 7-2-7-7-3 7-3z"/>,
  lock: <><rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/></>,
  logout: <><path d="M10 4H5a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h5M14 7l5 5-5 5M8 12h11"/></>,
  clock: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>, chevron: <path d="m9 18 6-6-6-6"/>, close: <path d="M5 5 19 19M19 5 5 19"/>,
  book: <><path d="M12 5v16M12 5C9 3 6 3 3 4v15c3-1 6-1 9 2 3-3 6-3 9-2V4c-3-1-6-1-9 1Z"/></>,
  device: <><rect x="5" y="5" width="14" height="14" rx="2"/><path d="M9 1v4M15 1v4M9 19v4M15 19v4M1 9h4M1 15h4M19 9h4M19 15h4M9 9h6v6H9z"/></>,
  sensor: <><circle cx="12" cy="12" r="3"/><path d="M6 6a9 9 0 0 0 0 12M18 6a9 9 0 0 1 0 12M3 3a13 13 0 0 0 0 18M21 3a13 13 0 0 1 0 18"/></>,
  workflow: <><rect x="3" y="3" width="6" height="6" rx="1"/><rect x="15" y="15" width="6" height="6" rx="1"/><path d="M6 9v9h9M9 6h9v9"/></>,
  refresh: <><path d="M20 7V3l-3 3a8 8 0 1 0 3 12M20 3h-4"/></>, send: <><path d="m3 11 18-8-8 18-2-8zM11 13l10-10"/></>,
  check: <><circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/></>, warning: <><path d="m12 3 10 18H2zM12 9v4M12 17h.01"/></>,
  error: <><circle cx="12" cy="12" r="9"/><path d="m9 9 6 6M15 9l-6 6"/></>, search: <><circle cx="10" cy="10" r="7"/><path d="m15 15 6 6"/></>, info: <><circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/></>,
}
export function Icon({ name, size = 20 }: { name: IconName; size?: number }) { return <svg aria-hidden="true" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{paths[name]}</svg> }