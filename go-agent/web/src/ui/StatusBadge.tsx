import { Icon, type IconName } from './Icon'
export type StatusTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral'
export function StatusBadge({ tone, label, icon, live = false }: { tone: StatusTone; label: string; icon?: IconName; live?: boolean }) { return <span data-jx-ui="" className={`jx-status jx-tone-${tone}`} role={live ? 'status' : undefined}>{icon && <Icon name={icon} size={16} />}{label}</span> }
