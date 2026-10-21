import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from './auth'
import AreasPage from './pages/AreasPage'
afterEach(()=>vi.unstubAllGlobals())
it('describes overlong directory searches without changing their draft or issuing a query',async()=>{
 const fetcher=vi.fn(async(url:string)=>url.endsWith('/auth/me')?Response.json({user:{id:'m1',username:'mei',display_name:'梅',role:'member',must_change_password:false},capabilities:['areas:read'],csrf_token:'test'}):Response.json({meta:{connection:'connected',freshness:'fresh',observed_at:null,last_attempt_at:null,last_success_at:null,error_code:null},totals:{devices:0,entities:0,standalone_entities:0},areas:[]}));vi.stubGlobal('fetch',fetcher)
 await act(async()=>render(<MemoryRouter><AuthProvider><AreasPage/></AuthProvider></MemoryRouter>));const field=screen.getByLabelText('搜索区域、设备或实体');fireEvent.change(field,{target:{value:'字'.repeat(129)}});fireEvent.click(screen.getByRole('button',{name:'搜索'}));expect(field).toHaveAccessibleDescription(/最多 128/);expect(field).toHaveAttribute('aria-invalid','true');expect(field).toHaveValue('字'.repeat(129));expect(fetcher.mock.calls.some(([url])=>url.includes('?q='))).toBe(false)
})
