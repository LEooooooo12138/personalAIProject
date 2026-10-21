import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { API } from 'typescript/unstable/sync'
import * as ts from 'typescript/unstable/ast'
const moduleExtensions = /\.(?:tsx?|jsx?|mjs|cjs)$/
const isTest = file => /(?:\.(?:test|spec)\.[cm]?[jt]sx?$|[\\/]__tests__[\\/])/.test(file)
const inside = (file, dir) => {const relative=path.relative(dir,file);return relative==='' || (!relative.startsWith('..'+path.sep) && relative!=='..' && !path.isAbsolute(relative))}
function runtimeFiles(dir){if(!fs.existsSync(dir))return [];return fs.readdirSync(dir,{withFileTypes:true}).flatMap(entry=>{const file=path.join(dir,entry.name);return entry.isDirectory()?runtimeFiles(file):moduleExtensions.test(file)&&!isTest(file)?[file]:[]})}
export function checkUIBoundaries({webRoot}) {
 webRoot=path.resolve(webRoot); const uiRoot=path.join(webRoot,'src/ui');const catalogRoot=path.join(uiRoot,'catalog')
 const files=runtimeFiles(uiRoot); if(!files.length)return []; const api=new API({cwd:webRoot}); const snapshot=api.updateSnapshot({openFiles:files})
 const errors=[];const visited=new Set()
 function visit(file,chain=[]){
  file=fs.realpathSync(file);if(visited.has(file))return;visited.add(file)
  const project=snapshot.getDefaultProjectForFile(file); const source=project?.program.getSourceFile(file); if(!source){errors.push('cannot parse source: '+file);return}
  function dependency(node){
   const specifier=node.text
   const catalog=inside(file,catalogRoot)
   if(specifier==='react'||specifier.startsWith('react/'))return
   if(catalog&&specifier==='react-dom/client')return
   let resolved
   if(specifier.startsWith('.')&&specifier.endsWith('.css')){const css=path.resolve(path.dirname(file),specifier);if(fs.existsSync(css))resolved=css}else resolved=project.checker.getSymbolAtLocation(node)?.declarations[0]?.path
   if(!resolved&&specifier.startsWith('.')&&specifier.endsWith('.css')){const css=path.resolve(path.dirname(file),specifier);if(fs.existsSync(css))resolved=css}
   const from=[...chain,path.relative(webRoot,file)].join(' -> ')
   if(!resolved){errors.push(`${from}: ${specifier} ${specifier.startsWith('.')?'cannot resolve':'package is not allowed'}`);return}
   resolved=fs.realpathSync(resolved)
   if(isTest(resolved)){errors.push(`${from}: runtime imports test module ${specifier}`);return}
   if(!catalog && inside(resolved,catalogRoot)){errors.push(`${from}: generic UI cannot import catalog ${specifier}`);return}
   if(!inside(resolved,uiRoot)){errors.push(`${from}: ${specifier} resolves outside UI (${path.relative(webRoot,resolved)})`);return}
   if(!specifier.startsWith('.')&&!inside(resolved,uiRoot)){errors.push(`${from}: package ${specifier} is not allowed`);return}
   if(moduleExtensions.test(resolved)&&!isTest(resolved))visit(resolved,[...chain,path.relative(webRoot,file)])
  }
  function walk(node){
   if((ts.isImportDeclaration(node)||ts.isExportDeclaration(node))&&node.moduleSpecifier){if(ts.isStringLiteral(node.moduleSpecifier))dependency(node.moduleSpecifier)}
   if(ts.isImportEqualsDeclaration(node)&&ts.isExternalModuleReference(node.moduleReference)&&node.moduleReference.expression&&ts.isStringLiteral(node.moduleReference.expression))dependency(node.moduleReference.expression)
   if(ts.isCallExpression(node)&&(node.expression.kind===ts.SyntaxKind.ImportKeyword||(ts.isIdentifier(node.expression)&&node.expression.text==='require'))){const arg=node.arguments[0];if(arg&&ts.isStringLiteral(arg))dependency(arg);else errors.push(`${path.relative(webRoot,file)}: dynamic import must be a string literal`)}
   node.forEachChild(walk)
  }
  walk(source)
 }
 try { for(const file of files)visit(file) } finally { snapshot.dispose(); api.close() }
 return errors
}
if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 const errors=checkUIBoundaries({webRoot:path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..')})
 if(errors.length){console.error(errors.join('\n'));process.exitCode=1}else console.log('UI boundaries passed: runtime components and catalog have no business dependencies.')
}
