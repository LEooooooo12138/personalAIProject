import type { TextareaHTMLAttributes, Ref } from 'react'
export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement> & { ref?: Ref<HTMLTextAreaElement>; invalid?: boolean }
export function Textarea({ invalid, className = '', ...props }: TextareaProps) { return <textarea {...props} data-jx-ui="" aria-invalid={invalid || props['aria-invalid']} className={`jx-textarea ${className}`} /> }
