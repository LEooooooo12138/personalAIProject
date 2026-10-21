import type { ReactNode } from 'react'
export function Breadcrumb({label='当前位置',children}:{label?:string;children:ReactNode}){return <nav data-jx-ui="" className="jx-breadcrumb" aria-label={label}>{children}</nav>}
