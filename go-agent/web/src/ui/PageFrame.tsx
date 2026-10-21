import type { ReactNode } from 'react'
export function PageFrame({ children, className = '' }: { children: ReactNode; className?: string }) { return <main data-jx-ui="" className={`jx-page ${className}`}>{children}</main> }
