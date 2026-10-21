import { useLayoutEffect, useRef, type ReactNode, type Ref } from 'react'
export function Disclosure({ title, defaultOpen = false, children, ref }: { title: string; defaultOpen?: boolean; children: ReactNode; ref?: Ref<HTMLDetailsElement> }) {
 const node = useRef<HTMLDetailsElement | null>(null)
 useLayoutEffect(() => { if (node.current) node.current.open = defaultOpen }, [])
 return <details data-jx-ui="" className="jx-disclosure" ref={element => { node.current = element; if (typeof ref === 'function') return ref(element); else if (ref) ref.current = element }} onInvalidCapture={() => { if (node.current) node.current.open = true }}><summary>{title}</summary><div className="jx-disclosure-content">{children}</div></details>
}
