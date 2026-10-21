import { useId, type InputHTMLAttributes, type ReactNode, type Ref } from 'react'
import { fieldDescriptionIds } from './Field'
export type CheckboxProps = Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & { label: ReactNode; hint?: ReactNode; ref?: Ref<HTMLInputElement> }
export function Checkbox({ label, hint, id, className = '', ...props }: CheckboxProps) {
 const generatedId = useId(); const fieldId = id || generatedId
 return <div data-jx-ui="" className={`jx-checkbox ${className}`}><label htmlFor={fieldId}><input {...props} id={fieldId} type="checkbox" aria-describedby={fieldDescriptionIds(fieldId, hint != null, false, props['aria-describedby'])} /><span>{label}</span></label>{hint != null && <div className="jx-field-hint" id={`${fieldId}-hint`}>{hint}</div>}</div>
}
