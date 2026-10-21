import { useId, useLayoutEffect, useRef, type ReactNode, type RefObject } from 'react'
import { IconButton } from './Button'
import { Icon } from './Icon'
export type DialogProps = { open: boolean; title: string; description?: ReactNode; dismissible?: boolean; onRequestClose: () => void; children?: ReactNode; footer?: ReactNode; initialFocusRef?: RefObject<HTMLElement | null>; returnFocusRef?: RefObject<HTMLElement | null>; restoreFocus?: boolean }
export function Dialog({ open, title, description, dismissible = true, onRequestClose, children, footer, initialFocusRef, returnFocusRef, restoreFocus = true }: DialogProps) {
 const id = useId(); const node = useRef<HTMLDialogElement>(null); const wasOpen = useRef(false); const trigger = useRef<HTMLElement | null>(null); const requested = useRef(false)
 useLayoutEffect(() => {
  const dialog = node.current; if (!dialog) return
  if (open && !wasOpen.current) { trigger.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; requested.current = false }
  if (open && !dialog.open) { dialog.showModal(); if (initialFocusRef?.current?.isConnected) initialFocusRef.current.focus() }
  if (!open && dialog.open) dialog.close()
  if (!open && wasOpen.current && restoreFocus) {
   const target = returnFocusRef?.current || trigger.current
   // This effect only runs while mounted. Unmount cleanup never steals focus.
   if (target?.isConnected && !target.hasAttribute('disabled') && !target.closest('[inert]')) target.focus()
  }
  wasOpen.current = open
 }, [open, initialFocusRef, returnFocusRef, restoreFocus])
 function requestClose() { if (!open || !dismissible || requested.current) return; requested.current = true; onRequestClose() }
 return <dialog data-jx-ui="" ref={node} className="jx-dialog" tabIndex={-1} aria-labelledby={`${id}-title`} aria-describedby={description != null ? `${id}-description` : undefined}
  onKeyDown={event => {
   if (event.key !== 'Tab') return
   const dialog = event.currentTarget
   const focusable = Array.from(dialog.querySelectorAll<HTMLElement>('button, a[href], input, select, textarea, summary, [tabindex]')).filter(element => element.tabIndex >= 0 && !element.hasAttribute('disabled') && !element.closest('[inert]') && element.getClientRects().length > 0)
   const first = focusable[0]; const last = focusable.at(-1)
   if (!first) { event.preventDefault(); dialog.focus() }
   else if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog)) { event.preventDefault(); last?.focus() }
   else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
  }}
  onCancel={event => { event.preventDefault(); requestClose() }}
  onClick={event => { if (event.target !== event.currentTarget) return; const bounds = event.currentTarget.getBoundingClientRect(); if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) requestClose() }}
  onSubmitCapture={event => { const form = event.target; const submitter = (event.nativeEvent as SubmitEvent).submitter; const method = submitter?.getAttribute('formmethod') || (form instanceof HTMLFormElement ? form.method : ''); if (method.toLowerCase() === 'dialog') { event.preventDefault(); requestClose() } }}>
  <div className="jx-dialog-header"><h2 id={`${id}-title`}>{title}</h2>{dismissible && <IconButton variant="ghost" label="关闭" onClick={requestClose}><Icon name="close" /></IconButton>}</div>
  <div className="jx-dialog-body">{description != null && <div className="jx-dialog-description" id={`${id}-description`}>{description}</div>}{children}</div>{footer != null && <div className="jx-dialog-footer">{footer}</div>}
 </dialog>
}
