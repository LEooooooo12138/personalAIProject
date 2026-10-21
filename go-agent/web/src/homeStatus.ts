import type { IntegrationsStatus } from './api'
import type { StatusTone } from './ui'
import { haStatusText } from './familyViews'
export function integrationStatusItems(status: IntegrationsStatus | null, readFailed: boolean): { id: string; label: string; tone: StatusTone; detail: string }[] {
  const ha = status?.ha, ai = status?.ollama
  return [
    { id: 'ha-connection', label: readFailed ? '本次检查未完成' : ha?.connection === 'connected' ? '最近检查可连接' : ha?.connection === 'unavailable' ? '暂时无法连接' : '连接尚未确认', tone: readFailed ? 'warning' : ha?.connection === 'connected' ? 'success' : ha?.connection === 'unavailable' ? 'danger' : 'neutral', detail: ha ? haStatusText(ha, readFailed) : '家庭设备服务的连接检查，与目录数据时间分别核对。' },
    { id: 'ha-freshness', label: readFailed || ha?.freshness === 'stale' ? '正在显示旧数据' : ha?.freshness === 'fresh' ? '最近目录数据' : '数据时间尚未确认', tone: readFailed || ha?.freshness === 'stale' ? 'warning' : 'neutral', detail: '目录是观察记录，不能代替现场检查。' },
    { id: 'ollama-connection', label: readFailed ? '本次检查未完成' : ai?.connection === 'connected' ? '最近检查成功' : ai?.connection === 'unavailable' ? '暂时无法连接' : '尚未确认', tone: readFailed ? 'warning' : ai?.connection === 'connected' ? 'success' : ai?.connection === 'unavailable' ? 'danger' : 'neutral', detail: '仅表示本机模型服务连接检查结果。' },
    { id: 'ollama-models', label: !ai?.models.length || readFailed || ai.connection !== 'connected' ? '模型安装暂无法确认' : ai.models.some((model) => model.available === false) ? '有模型未安装' : ai.models.some((model) => model.available === null) ? '部分安装情况未确认' : '模型已安装', tone: 'neutral', detail: '安装检查不代表实际生成已验证。' },
  ]
}
