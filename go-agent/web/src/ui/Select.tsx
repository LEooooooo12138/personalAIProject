import type { SelectHTMLAttributes, Ref } from 'react'
export type SelectProps = SelectHTMLAttributes<HTMLSelectElement> & { ref?: Ref<HTMLSelectElement>; invalid?: boolean }
export function Select({ invalid, className = '', ...props }: SelectProps) { return <select {...props} data-jx-ui="" aria-invalid={invalid || props['aria-invalid']} className={`jx-select ${className}`} /> }
