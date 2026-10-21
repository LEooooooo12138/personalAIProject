import type { InstallOperationView, SuggestionView } from './api'
import { dateLabel } from './familyViews'
import { KeyValueList, Stack } from './ui'

export function installationLabel(item: SuggestionView): string {
 const op = item.installation
 if (item.installation_state === 'deleted' || op?.deleted_at) return '已删除，保留历史记录'
 if (op?.historical) return '历史安装版本，只读记录'
 if (item.installation_state === 'legacy_unverified' || op?.legacy_unverified || item.status === 'confirmed' && !op && item.installation_state !== 'verified') return '历史安装记录，当前归属与加载未核实'
 const status = op?.status ?? item.status
 if (status === 'confirmed') return op?.archive_status === 'pending' ? '安装已核实，本地归档待完成' : op?.archive_status === 'complete' ? '已核实安装并归档' : '安装已核实（最近记录）'
 if (op?.config_status === 'drift' || op?.load_status === 'drift') return '配置或加载版本已变化，待核对'
 if ((op?.config_status === 'saved' || op?.config_status === 'matching') && op.load_status !== 'verified') return '配置已保存，加载结果待核对'
 const labels = { pending: '待审核', superseded: '已被新版本替代', applying: '安装进行中，尚未核实', failed: '安装请求已明确拒绝，未应用', unknown: '安装结果未知，需只读核对', ignored: '已忽略，未执行' }
 return labels[status]
}
export function installationTone(item: SuggestionView): 'danger' | 'warning' | 'success' | 'neutral' {
 if (item.installation_state === 'deleted' || item.installation?.historical) return 'neutral'
 if (item.installation_state === 'legacy_unverified' || item.status === 'confirmed' && !item.installation && item.installation_state !== 'verified') return 'warning'
 const status = item.installation?.status ?? item.status
 if (status === 'failed') return 'danger'
 if (status === 'unknown' || status === 'applying' || item.installation?.archive_status === 'pending') return 'warning'
 return status === 'confirmed' ? 'success' : 'neutral'
}
export function AutomationInstallation({ operation: op }: { operation: InstallOperationView }) {
 if (op.historical) return <Stack gap={16}><p>{op.deleted_at ? `已删除，记录时间：${dateLabel(op.deleted_at)}。` : '此安装版本已被更新。'}这里只保留历史安装记录，不能据此判断当前实体的归属或启用状态。</p><p>历史版本：<code>{op.suggestion_version}</code></p></Stack>
 const config = { unknown: '尚未核实', saved: '已收到保存响应', matching: '存储配置与确认版本一致', drift: '存储配置与确认版本不同', missing: '未读取到存储配置' }
 const load = { unknown: '尚未核实', verified: '最近读取时加载版本一致', drift: '当前读取的加载版本不同' }
 const archive = { not_ready: '待安装核实后归档', pending: '本地归档待完成', complete: '本地归档已完成' }
 const state = op.observed_state === 'on' ? '已启用（最近观察）' : op.observed_state === 'off' ? '已停用（最近观察）' : '尚未核实'
 return <Stack gap={16}><KeyValueList items={[
  { id: 'config', label: '配置保存', value: config[op.config_status] },
  { id: 'mapping', label: '实体映射', value: op.mapping_status === 'verified' ? '已核实映射' : '尚未核实' },
  { id: 'load', label: '加载版本', value: load[op.load_status] },
  { id: 'state', label: '启用状态', value: state },
  { id: 'archive', label: '本地归档', value: archive[op.archive_status] },
  { id: 'observed', label: '最近加载核对', value: op.observed_at ? dateLabel(op.observed_at) : '暂无记录' },
 ]}/><p>安装、启用、触发和设备动作是不同事实。核对只读取 HA 状态；安装已核实后可补全本地归档，不会重新创建或重载自动化。</p></Stack>
}
