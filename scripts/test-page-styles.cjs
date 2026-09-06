// Run against Vite with VITE_DEMO_MODE=true. Requires Playwright and Edge.
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
async function main() {
  const url = process.argv[2] || 'http://127.0.0.1:15175'
  const output = process.argv[3] || 'dist/layout-verification/style-audit'
  await fs.mkdir(output, { recursive: true })
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    for (const width of [1440, 1024, 512]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } })
      await page.clock.setFixedTime(new Date('2026-08-29T08:40:00Z'))
      for (const route of ['/', '/tickets', '/match-analysis', '/rules']) {
        await page.goto(url + route)
        await page.locator('.panel:visible').first().waitFor()
        if (route === '/') {
          await page.locator('.match-analysis-canvas').waitFor()
          await page.locator('.chart-panel summary').click()
          const inset = await page.locator('.chart-panel').evaluate((panel) => {
            const box = panel.getBoundingClientRect()
            return [...panel.querySelectorAll('.match-analysis-export, .analysis-field-description, summary')].map((item) => item.getBoundingClientRect().left - box.left)
          })
          assert(inset.length === 3 && inset.every((n) => n >= 20), JSON.stringify(inset))
        }
        if (route === '/tickets') {
          await page.getByRole('button', { name: '+ 新建 / 批量生成', exact: true }).click()
          await page.getByRole('button', { name: '批量与持续流量', exact: true }).click()
          await page.locator('.generator-card').first().waitFor()
          await page.locator('.generator-card input[type=checkbox]').first().check()
        }
        if (route === '/match-analysis') await page.locator('.analysis-stat-panel').waitFor()
        if (route === '/rules') await page.locator('.rule-form-panel').first().waitFor()
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${route} ${width}px overflows`)
        await page.evaluate(() => window.scrollTo(0, 0))
        await page.screenshot({ path: path.join(output, `${route.replaceAll('/', '') || 'dashboard'}-${width}.png`), fullPage: true })
        if (route === '/') {
          await page.locator('.match-row-button').first().click()
          const dialog = page.getByRole('dialog')
          await dialog.waitFor()
          assert(await dialog.evaluate((el) => { const r = el.getBoundingClientRect(); return r.left >= 0 && r.right <= innerWidth }), 'detail drawer outside viewport')
          await dialog.getByRole('button', { name: '关闭详情', exact: true }).first().click()
          await page.clock.setFixedTime(new Date('2026-09-06T00:00:00Z'))
          await page.reload()
          await page.getByText('最近 30 分钟没有已保留比赛', { exact: true }).waitFor()
          await page.clock.setFixedTime(new Date('2026-08-29T08:40:00Z'))
        }
        if (route === '/rules') {
          const tabs = page.getByRole('tab')
          for (let i = 0; i < await tabs.count(); i++) {
            await tabs.nth(i).click()
            assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `rules tab ${i} overflows at ${width}`)
          }
        }
        console.log(`PASS ${route} ${width}px`)
      }
      await page.close()
    }
  } finally { await browser.close() }
}
main().catch((error) => { console.error(error); process.exitCode = 1 })
