import { Icon } from './Icon'
export function Avatar({ label, size = 'regular' }: { label: string; size?: 'regular' | 'large' }) {
 const initial = Array.from(label.trim())[0]
 return <span data-jx-ui="" className={`jx-avatar jx-avatar-${size}`} role="img" aria-label={label.trim() || '成员'}>{initial || <Icon name="user" />}</span>
}
