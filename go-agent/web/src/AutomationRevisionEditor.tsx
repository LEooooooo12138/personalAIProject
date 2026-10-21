import type { SuggestionView } from './api'
import { useConsoleAction } from './consoleActions'
import { AutomationRevisionForm } from './AutomationRevisionForm'

export function AutomationRevisionEditor({ suggestion, onSaved, onCancel }: { suggestion: SuggestionView; onSaved: (next: SuggestionView) => void; onCancel: () => void }) {
  const action = useConsoleAction()
  if (!suggestion.intent) return null
  return <AutomationRevisionForm version={{ id: suggestion.id, intent: suggestion.intent, presence_binding: suggestion.presence_binding, diff: suggestion.revision_diff ?? [] }} pending={action.pending} error={action.error} enabled={Boolean(suggestion.can_revise) && suggestion.status === 'pending' && !suggestion.installation} onCancel={onCancel} onSave={async body => {
    const result = await action.run<{ suggestion: SuggestionView }>('/admin/suggestions/' + encodeURIComponent(suggestion.id) + '/revisions', body)
    if (result) onSaved(result.suggestion)
  }}/>
}
