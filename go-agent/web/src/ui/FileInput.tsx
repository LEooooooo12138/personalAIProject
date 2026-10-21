import type { InputHTMLAttributes, Ref } from 'react'
export type FileInputProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type' | 'value' | 'defaultValue'> & { ref?: Ref<HTMLInputElement>; invalid?: boolean }
export function FileInput({ invalid, className = '', ...props }: FileInputProps) { return <input {...props} data-jx-ui="" type="file" aria-invalid={invalid || props['aria-invalid']} className={`jx-file-input ${className}`} /> }
