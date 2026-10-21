import { createRef } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { Button, IconButton, ActionLink } from './Button'
it('uses a safe default button type and preserves explicit submit identity', async () => {
 const submit=vi.fn((event: React.FormEvent)=>event.preventDefault()); const ref=createRef<HTMLButtonElement>(); const user=userEvent.setup()
 render(<form id="sample" onSubmit={submit}><Button ref={ref}>Preview</Button><Button type="submit" name="intent" value="save" form="sample">Save</Button></form>)
 await user.click(screen.getByRole('button',{name:'Preview'})); expect(submit).not.toHaveBeenCalled(); expect(ref.current?.type).toBe('button')
 const save=screen.getByRole('button',{name:'Save'}); expect(save).toHaveAttribute('name','intent'); expect(save).toHaveAttribute('form','sample'); await user.click(save); expect(submit).toHaveBeenCalledTimes(1)
})
it('prevents both pointer and keyboard activation while busy or disabled', async () => {
 const activate=vi.fn(); const user=userEvent.setup(); render(<><Button busy onClick={activate}>Save</Button><Button disabled onClick={activate}>Delete</Button><IconButton label="Refresh" onClick={activate}>↻</IconButton><ActionLink href="#details">Details</ActionLink></>)
 for(const name of ['Save','Delete']) {const button=screen.getByRole('button',{name}); expect(button).toBeDisabled(); fireEvent.click(button)}
 await user.tab(); expect(screen.getByRole('button',{name:'Refresh'})).toHaveFocus(); await user.keyboard('{Enter}'); expect(activate).toHaveBeenCalledTimes(1); expect(screen.getByRole('link',{name:'Details'})).toHaveAttribute('href','#details'); expect(screen.getByRole('button',{name:'Save'})).toHaveAttribute('aria-busy','true')
})
