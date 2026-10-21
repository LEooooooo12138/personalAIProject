import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { checkUIBoundaries } from './check-ui-boundaries.mjs'
function fixture(files){const root=fs.mkdtempSync(path.join(os.tmpdir(),'jx-boundary-'));for(const [file,source] of Object.entries(files)){const target=path.join(root,file);fs.mkdirSync(path.dirname(target),{recursive:true});fs.writeFileSync(target,source)}return root}
function inspect(files){const root=fixture(files);try{return checkUIBoundaries({webRoot:root})}finally{fs.rmSync(root,{recursive:true,force:true})}}
test('React, pure UI barrels and local CSS are allowed',()=>{assert.deepEqual(inspect({'src/ui/index.ts':"export * from './Button'",'src/ui/Button.tsx':"import type {ReactNode} from 'react'; import './ui.css'; export const label='safe'",'src/ui/ui.css':'button{color:green}'}),[])})
for(const source of ["import '../auth'", "export * from '../api'", "const action=()=>import('../consoleActions')"])test(`rejects business dependency ${source}`,()=>{assert.match(inspect({'src/ui/Button.tsx':source,'src/auth.ts':'export {}','src/api.ts':'export {}','src/consoleActions.ts':'export {}'}).join('\n'),/outside UI/)})
test('follows a UI barrel into business modules',()=>{assert.match(inspect({'src/ui/catalog/Catalog.tsx':"import '../bridge'",'src/ui/bridge.ts':"export * from '../familyQuery'",'src/familyQuery.ts':'export {}'}).join('\n'),/familyQuery/)})
test('rejects nonliteral dynamic imports rather than guessing',()=>{assert.match(inspect({'src/ui/unsafe.ts':'const load=(moduleName)=>import(moduleName)'}).join('\n'),/dynamic import must be a string literal/)})
test('rejects Router and network-capable external packages',()=>{assert.match(inspect({'src/ui/unsafe.ts':"import {Link} from 'react-router-dom'"}).join('\n'),/not allowed/)})
test('catalog permits client entry but generic UI cannot import react-dom',()=>{assert.deepEqual(inspect({'src/ui/catalog/main.tsx':"import {createRoot} from 'react-dom/client'"}),[]);assert.match(inspect({'src/ui/core.ts':"import 'react-dom/client'"}).join('\n'),/not allowed/)})
test('tests are excluded and unresolved local imports fail closed',()=>{assert.deepEqual(inspect({'src/ui/Button.test.tsx':"import '../auth'"}),[]);assert.match(inspect({'src/ui/Button.tsx':"import './missing'"}).join('\n'),/cannot resolve/)})
test('runtime imports cannot smuggle business code through excluded tests',()=>{assert.match(inspect({'src/ui/Button.tsx':"import './bridge.test'",'src/ui/bridge.test.ts':"import '../auth'",'src/auth.ts':'export {}'}).join('\n'),/test module/)})
test('generic UI cannot import or reexport the development catalog',()=>{assert.match(inspect({'src/ui/index.ts':"export * from './catalog/Catalog'",'src/ui/catalog/Catalog.ts':'export const sample=1'}).join('\n'),/catalog/)})
test('compiler resolves aliases before checking their destination',()=>{assert.match(inspect({'tsconfig.json':JSON.stringify({compilerOptions:{paths:{'@business/*':['./src/*']}},include:['src']}),'src/ui/Button.ts':"import '@business/auth'",'src/auth.ts':'export {}'}).join('\n'),/outside UI/)})
test('catalog cannot restore retired legacy styles from outside UI',()=>{assert.match(inspect({'src/ui/catalog/main.tsx':"import '../../styles/legacy.css'",'src/styles/legacy.css':'button{color:red}'}).join('\n'),/outside UI/)})
