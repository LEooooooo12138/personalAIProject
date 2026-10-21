import { test, expect } from '@playwright/test'
test('actual brand and typography use the independent modern foundation', async ({page})=>{
 await page.goto('/app/catalog/index.html?case=foundation'); await expect(page.getByRole('heading',{name:'视觉基础'})).toBeVisible()
 const modern=page.getByRole('button',{name:'新组件按钮'}); await expect(modern).toHaveCSS('background-color','rgb(23, 107, 91)'); await expect(modern).toHaveCSS('font-size','16px'); await expect(page.getByLabel('新组件字段')).toHaveCSS('border-top-color','rgb(113, 134, 123)'); await expect(page.getByTestId('hidden-sample')).toBeHidden(); await expect(page.getByRole('heading',{name:'统一页面'})).toHaveCSS('font-size','30px')
 const colors=await page.evaluate(()=>['canvas','text','text-muted','brand-hover','field-border'].map(key=>{const element=document.createElement('span');element.style.color=`var(--jx-color-${key})`;document.body.append(element);const value=getComputedStyle(element).color;element.remove();return value}));expect(colors).toEqual(['rgb(246, 248, 245)','rgb(32, 53, 47)','rgb(83, 101, 94)','rgb(18, 87, 72)','rgb(113, 134, 123)'])
})
