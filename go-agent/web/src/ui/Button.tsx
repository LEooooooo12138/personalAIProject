import type { ButtonHTMLAttributes, AnchorHTMLAttributes, ReactNode, Ref } from 'react'
export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
export type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & { ref?: Ref<HTMLButtonElement>; variant?: ButtonVariant; size?: 'regular' | 'comfortable'; busy?: boolean }
export function Button({ variant = 'primary', size = 'regular', busy = false, disabled, type = 'button', className = '', ...props }: ButtonProps) {
 return <button {...props} data-jx-ui="" type={type} disabled={disabled || busy} aria-busy={busy || undefined} className={`jx-button jx-button-${variant} jx-button-${size} ${className}`} />
}
export function IconButton({ label, children, className = '', ...props }: ButtonProps & { label: string; children: ReactNode }) {
 return <Button {...props} aria-label={label} className={`jx-icon-button ${className}`}>{children}</Button>
}
export function ActionLink({ variant = 'secondary', className = '', ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement>; variant?: ButtonVariant }) {
 return <a {...props} data-jx-ui="" className={`jx-action-link jx-button-${variant} ${className}`} />
}
