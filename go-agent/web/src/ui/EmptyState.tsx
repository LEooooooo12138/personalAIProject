import type { ReactNode } from 'react'
import { Icon, type IconName } from './Icon'
export function EmptyState({ title, description, icon, actions }: { title: string; description?: ReactNode; icon?: IconName; actions?: ReactNode }) { return <div data-jx-ui="" className="jx-empty">{icon && <Icon name={icon} size={32}/>}<h2>{title}</h2>{description && <div className="jx-muted">{description}</div>}{actions}</div> }
