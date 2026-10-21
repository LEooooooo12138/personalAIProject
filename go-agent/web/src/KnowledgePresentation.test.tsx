import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { KnowledgeBrowser } from './knowledgeViews'
import { AuthProvider } from './auth'
afterEach(()=>vi.unstubAllGlobals())
it('associates a public search validation failure with the editable field',async()=>{
 vi.stubGlobal('fetch',vi.fn(async(url:string)=>url.endsWith('/auth/me')?Response.json({user:{id:'m1',username:'mei',display_name:'梅',role:'member',must_change_password:false},capabilities:['knowledge:read'],csrf_token:'test'}):Response.json({})))
 await act(async()=>render(<MemoryRouter initialEntries={['/app/knowledge']}><AuthProvider><KnowledgeBrowser/></AuthProvider></MemoryRouter>))
 const field=screen.getByLabelText('搜索公开知识');await act(async()=>fireEvent.change(field,{target:{value:'字'.repeat(201)}}));await act(async()=>fireEvent.click(screen.getByRole('button',{name:'搜索知识'})))
 expect(field).toHaveAttribute('aria-invalid','true');expect(field).toHaveAccessibleDescription(/1 至 200/);expect(screen.getByRole('button',{name:'搜索知识'})).toHaveAttribute('type','submit');expect(field).toHaveValue('字'.repeat(201))
})
