import type { ReactNode } from 'react'
export function navigationItemClass(active:boolean):string{return `jx-navigation-item${active?' jx-navigation-active':''}`}
export function Navigation({label,variant,children}:{label:string;variant:'side'|'bottom';children:ReactNode}){return <nav data-jx-ui="" className={`jx-navigation jx-navigation-${variant}`} aria-label={label}>{children}</nav>}
