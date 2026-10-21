import type { ReactNode } from 'react'
export function KeyValueList({items}:{items:readonly {id:string;label:string;value:ReactNode}[]}){return <dl data-jx-ui="" className="jx-key-values">{items.map(item=><div key={item.id}><dt>{item.label}</dt><dd>{item.value}</dd></div>)}</dl>}
