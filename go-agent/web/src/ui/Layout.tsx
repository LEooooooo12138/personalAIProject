import type { ReactNode } from 'react'
export type Gap = 8 | 12 | 16 | 24 | 32
export type LayoutProps = { children: ReactNode; gap?: Gap }
export function Stack({children,gap=16}:LayoutProps){return <div data-jx-ui="" className={`jx-stack jx-gap-${gap}`}>{children}</div>}
export function Grid({children,gap=16,columns=2}:LayoutProps & {columns?:1|2|3}){return <div data-jx-ui="" className={`jx-grid jx-grid-${columns} jx-gap-${gap}`}>{children}</div>}
export function ActionGroup({children,gap=12}:LayoutProps){return <div data-jx-ui="" className={`jx-actions jx-gap-${gap}`}>{children}</div>}
