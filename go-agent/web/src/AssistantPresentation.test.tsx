import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from './auth'
import AssistantPage from './pages/AssistantPage'
const settings={revision:'r1',updated_at:'',key_configured:false,llm_mode:'local',asr_mode:'local',tts_mode:'local',local_asr_url:'http://127.0.0.1:8000/asr',local_tts_url:'http://127.0.0.1:8000/tts'}
afterEach(()=>vi.unstubAllGlobals())
function show(){const writes=vi.fn();vi.stubGlobal('fetch',vi.fn(async(url:string,options?:RequestInit)=>{if(options?.method==='PUT'){writes(JSON.parse(options.body as string));return Response.json({...settings,revision:'r2'})}if(url.endsWith('/auth/me'))return Response.json({user:{id:'a1',username:'owner',display_name:'家长',role:'admin',must_change_password:false},capabilities:['providers:manage'],csrf_token:'test'});return Response.json(settings)}));render(<MemoryRouter><AuthProvider><AssistantPage/></AuthProvider></MemoryRouter>);return writes}
it('mode changes preserve address drafts and require explicit saving',async()=>{const writes=show();const address=await screen.findByLabelText('本地 ASR 服务地址');fireEvent.change(address,{target:{value:'http://127.0.0.1:9000/asr'}});fireEvent.change(screen.getByLabelText('ASR 模式'),{target:{value:'cloud'}});fireEvent.change(screen.getByLabelText('ASR 模式'),{target:{value:'hybrid'}});expect(address).toHaveValue('http://127.0.0.1:9000/asr');expect(writes).not.toHaveBeenCalled();fireEvent.click(screen.getByRole('button',{name:'保存服务配置'}));await waitFor(()=>expect(writes).toHaveBeenCalledWith(expect.objectContaining({asr_mode:'hybrid',local_asr_url:'http://127.0.0.1:9000/asr'})))})
it('native invalid advanced fields expand before focus without saving',async()=>{const writes=show();const address=await screen.findByLabelText('本地 ASR 服务地址');const details=address.closest('details');expect(details).not.toBeNull();expect(details?.open).toBe(false);await act(async()=>fireEvent.invalid(address));expect(details?.open).toBe(true);expect(writes).not.toHaveBeenCalled()})
it('periodic reads do not overwrite a draft or silently adopt a new revision',async()=>{
 const writes=show();const address=await screen.findByLabelText('本地 ASR 服务地址');fireEvent.change(address,{target:{value:'http://127.0.0.1:9999/asr'}})
 let refreshed=false;const original=vi.mocked(fetch).getMockImplementation()!;vi.mocked(fetch).mockImplementation(async(_url,options)=>{if(String(_url).endsWith('/auth/me'))return original(_url,options);if(options?.method==='PUT'){writes(JSON.parse(options.body as string));return Response.json({...settings,revision:'r3'})}refreshed=true;return Response.json({...settings,revision:'r9',local_asr_url:'http://127.0.0.1:8080/server'})})
 await act(async()=>fireEvent(document,new Event('visibilitychange')));await waitFor(()=>expect(refreshed).toBe(true));expect(address).toHaveValue('http://127.0.0.1:9999/asr');fireEvent.click(screen.getByRole('button',{name:'保存服务配置'}));await waitFor(()=>expect(writes).toHaveBeenCalledWith(expect.objectContaining({expected_revision:'r1',local_asr_url:'http://127.0.0.1:9999/asr'})))
})
it('revision conflicts preserve configuration drafts and do not retry writes',async()=>{
 show();const mode=await screen.findByLabelText('LLM 模式');fireEvent.change(mode,{target:{value:'hybrid'}});let writes=0;vi.mocked(fetch).mockImplementation(async(_url,options)=>{if(options?.method==='PUT')writes++;return Response.json({error:{code:'revision_conflict'}},{status:409})});fireEvent.click(screen.getByRole('button',{name:'保存服务配置'}));expect(await screen.findByRole('alert')).toHaveTextContent('重新读取后保存');expect(mode).toHaveValue('hybrid');expect(writes).toBe(1);expect(screen.queryByText('服务配置已保存。')).not.toBeInTheDocument()
})

it.each([
 ['本地 ASR 服务地址', 'local_asr_url_invalid', 'local-asr'],
 ['本地 TTS 服务地址', 'local_tts_url_invalid', 'local-tts'],
])('server validation reveals and focuses %s while preserving the draft', async(label, code, id)=>{
 const writes=show()
 const address=await screen.findByLabelText(label)
 fireEvent.change(address,{target:{value:'http://example.invalid/voice'}})
 const details=address.closest('details')!
 const original=vi.mocked(fetch).getMockImplementation()!
 vi.mocked(fetch).mockImplementation(async(url,options)=>{
  if(options?.method==='PUT'){writes(JSON.parse(options.body as string));return Response.json({error:{code}},{status:400})}
  return original(url,options)
 })
 expect(details.open).toBe(false)
 fireEvent.click(screen.getByRole('button',{name:'保存服务配置'}))
 await waitFor(()=>expect(address).toHaveAttribute('aria-invalid','true'))
 expect(details.open).toBe(true)
 expect(address).toHaveFocus()
 expect(address).toHaveAttribute('aria-describedby',id+'-error')
 expect(document.getElementById(id+'-error')).toHaveTextContent('HTTP 回环 IP')
 expect(address).toHaveValue('http://example.invalid/voice')
 expect(writes).toHaveBeenCalledTimes(1)
 fireEvent.change(address,{target:{value:'http://127.0.0.1:9000/corrected'}})
 expect(address).not.toHaveAttribute('aria-invalid','true')
 expect(address).not.toHaveAttribute('aria-describedby')
 vi.mocked(fetch).mockImplementation(original)
 fireEvent.click(screen.getByRole('button',{name:'保存服务配置'}))
 expect(await screen.findByText('服务配置已保存。')).toBeInTheDocument()
 expect(writes).toHaveBeenCalledTimes(2)
})
it.each([['revision_conflict',409],['unavailable',503]] as const)('unrelated %s errors do not mark or reveal local address fields',async(code,status)=>{
 show()
 const address=await screen.findByLabelText('本地 ASR 服务地址')
 fireEvent.change(address,{target:{value:'http://127.0.0.1:9999/draft'}})
 vi.mocked(fetch).mockImplementation(async()=>Response.json({error:{code}},{status}))
 fireEvent.click(screen.getByRole('button',{name:'保存服务配置'}))
 await screen.findByRole('alert')
 expect(address.closest('details')?.open).toBe(false)
 expect(address).not.toHaveAttribute('aria-invalid','true')
 expect(address).toHaveValue('http://127.0.0.1:9999/draft')
})
