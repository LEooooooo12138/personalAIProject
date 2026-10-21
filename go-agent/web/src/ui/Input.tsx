import type { InputHTMLAttributes, Ref } from 'react'
export type InputProps = InputHTMLAttributes<HTMLInputElement> & { ref?: Ref<HTMLInputElement>; invalid?: boolean }
export function Input({ invalid, className = '', ...props }: InputProps) { return <input {...props} data-jx-ui="" aria-invalid={invalid || props['aria-invalid']} className={`jx-input ${className}`} /> }
