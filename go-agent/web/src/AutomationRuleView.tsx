import type { SuggestionRule, SuggestionRevisionChange } from './api'
import { summarizeRule, automationEntityLabel, type AutomationEntity } from './automationPresentation'
import { Alert, Card, Disclosure, Icon, KeyValueList, Stack } from './ui'
export function AutomationRuleView({ rule, entities }: { rule: SuggestionRule | null; entities: ReadonlyMap<string, AutomationEntity> }) {
  if (!rule) return <Alert tone="warning">当前没有完整可执行规则，补齐条件后再审阅。</Alert>
  const summary = summarizeRule(rule, entities)
  return <Stack gap={16}>
    {summary.supported ? <ol className="jx-rule-flow" aria-label="规则关系">
      <li className="jx-rule-stage"><h3><Icon name="clock"/>何时 · 任一触发</h3><ul>{summary.triggers.map((text, index) => <li key={index}>{text}</li>)}</ul></li>
      <li className="jx-rule-stage"><h3><Icon name="check"/>条件 · 全部满足</h3>{summary.conditions.length ? <ul>{summary.conditions.map((text, index) => <li key={index}>{text}</li>)}</ul> : <p>无额外条件</p>}</li>
      <li className="jx-rule-stage"><h3><Icon name="workflow"/>动作 · 按顺序</h3><ol>{summary.actions.map((text, index) => <li key={index}>{text}</li>)}</ol></li>
    </ol> : <Alert tone="warning">规则结构包含未支持或无法核对的内容，请查看原始规则。摘要不能作为执行授权。</Alert>}
    {summary.unresolvedEntityIds.length > 0 && <p className="jx-muted">部分实体名称尚未核对，请检查设备目录；技术标识可在原始规则中查看。</p>}
    <p className="jx-muted">这是规则关系，不能证明自动化已经执行。</p>
    <Disclosure title="原始规则">{summary.supported && <KeyValueList items={rule.actions.map((action, index) => ({ id: String(index), label: '服务 ' + (index + 1), value: <code>{action.service}</code> }))}/>}<pre>{JSON.stringify(rule, null, 2)}</pre></Disclosure>
  </Stack>
}

export function AutomationRevisionDiff({ changes, entities }: { changes: SuggestionRevisionChange[]; entities: ReadonlyMap<string, AutomationEntity> }) {
  const labels = { time: '触发时间', source: '触发源', target: '动作目标', presence_entity: '家庭占用实体', presence_state: '有人在家状态' }
  function value(change: SuggestionRevisionChange, raw: string) {
    if (!raw) return '未设置'
    if (['source', 'target', 'presence_entity'].includes(change.field)) {
      const entity = entities.get(raw)
      return automationEntityLabel(entity)
    }
    if (change.field === 'presence_state') return raw === 'on' ? '开启' : raw === 'home' ? '在家' : raw
    return raw
  }
  return <Card heading="版本差异"><Stack gap={16}>
    <KeyValueList items={changes.map(change => ({ id: change.field, label: labels[change.field], value: <><span>原版本：{value(change, change.before)}</span><br/><span>新版本：{value(change, change.after)}</span></> }))}/>
    <p>强制条件、家庭时区与开启动作保持不变。原版本历史证据保留在原版本；新版尚未经过历史验证。保存修订不会安装自动化。</p>
    <Disclosure title="差异技术详情"><pre>{JSON.stringify(changes, null, 2)}</pre></Disclosure>
  </Stack></Card>
}
