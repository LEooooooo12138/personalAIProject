import { createRef } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { Field, fieldDescriptionIds } from './Field'
import { Input } from './Input'
import { Textarea } from './Textarea'
import { Select } from './Select'
import { Checkbox } from './Checkbox'
import { FileInput } from './FileInput'
import { Disclosure } from './Disclosure'
it('links only rendered descriptions and leaves required semantics explicit',()=>{
 render(<Field id="name" label="Name" hint="Use a nickname" error="Already used" required><Input id="name" aria-describedby={fieldDescriptionIds('name',true,true,'safety name-hint')} invalid /></Field>)
 const input=screen.getByLabelText(/Name/); expect(input).not.toBeRequired(); expect(input).toHaveAccessibleDescription('safety Use a nickname Already used'.replace('safety ','')); expect(input).toHaveAttribute('aria-describedby','safety name-hint name-error'); expect(input).toHaveAttribute('aria-invalid','true'); expect(fieldDescriptionIds('n',false,false)).toBeUndefined()
})
it('preserves native form values, refs and composition events',()=>{
 const ref=createRef<HTMLInputElement>(); const textRef=createRef<HTMLTextAreaElement>(); const composition=vi.fn(); const key=vi.fn()
 render(<form data-testid="form"><Input ref={ref} name="name" defaultValue="Mei" required /><Textarea ref={textRef} name="message" defaultValue="Hello" onCompositionStart={composition} onKeyDown={key} /><Select name="role" defaultValue="member"><option value="member">Member</option></Select><Checkbox name="share" value="yes" label="Share" defaultChecked /><FileInput name="file" accept=".txt" /></form>)
 const data=new FormData(screen.getByTestId('form') as HTMLFormElement); expect(data.get('name')).toBe('Mei'); expect(data.get('message')).toBe('Hello'); expect(data.get('role')).toBe('member'); expect(data.get('share')).toBe('yes'); expect(ref.current).toBeRequired(); fireEvent.compositionStart(textRef.current!); fireEvent.keyDown(textRef.current!,{key:'Enter'}); expect(composition).toHaveBeenCalledOnce(); expect(key).toHaveBeenCalledOnce()
})
it('keeps collapsed fields mounted and expands before native invalid focus',()=>{
 const ref=createRef<HTMLDetailsElement>(); render(<Disclosure title="Details" ref={ref}><Input aria-label="Required" required /></Disclosure>); expect(ref.current?.open).toBe(false); const field=screen.getByLabelText('Required'); fireEvent.invalid(field); expect(ref.current?.open).toBe(true); expect(field).toBeInTheDocument()
})
