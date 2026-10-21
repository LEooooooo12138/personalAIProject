import type { ReactNode } from 'react'
export function fieldDescriptionIds(id: string, hasHint: boolean, hasError: boolean, existingIds?: string): string | undefined {
 const ids = [...(existingIds?.split(/\s+/).filter(Boolean) || []), ...(hasHint ? [`${id}-hint`] : []), ...(hasError ? [`${id}-error`] : [])]
 return [...new Set(ids)].join(' ') || undefined
}
export function Field({ id, label, hint, error, required, children }: { id: string; label: ReactNode; hint?: ReactNode; error?: string; required?: boolean; children: ReactNode }) {
 return <div data-jx-ui="" className="jx-field"><div className="jx-field-label"><label htmlFor={id}>{label}</label>{required && <span aria-hidden="true"> *</span>}</div>{children}{hint != null && <div id={`${id}-hint`} className="jx-field-hint">{hint}</div>}{error && <div id={`${id}-error`} className="jx-field-error">{error}</div>}</div>
}
