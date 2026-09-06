// Requires Playwright (resolvable through NODE_PATH) and a running Web/API pair.
// Usage: node scripts/test-client-layout.cjs http://127.0.0.1:15173 [screenshot-dir]
const { chromium } = require('playwright')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')

async function main() {
  const url = process.argv[2] || 'http://127.0.0.1:15173'
  const output = process.argv[3]
  if (output) await fs.mkdir(output, { recursive: true })
  const browser = await chromium.launch({ channel: 'msedge', headless: true })
  try {
    for (const width of [1440, 1024, 512]) {
      const page = await browser.newPage({ viewport: { width, height: 1000 } })
      // Only the desktop IPC boundary is stubbed; pages use the running real API.
      await page.addInitScript(() => {
        window.__MATCH_DESKTOP__ = true
        window.__TAURI_INTERNALS__ = {
          invoke: async (command) => {
            if (command === 'desktop_update_status') return null
            if (command === 'check_desktop_update') return {
              currentVersion: '0.1.0', version: '0.1.0', available: false,
              repository: 'fixture/layout', notes: '', assetName: null,
            }
            throw new Error(`Unexpected command: ${command}`)
          },
        }
      })
      await page.goto(url)
      await page.locator('.topology-overview').waitFor()
      const spacing = await page.evaluate(() => {
        const rect = (selector) => document.querySelector(selector).getBoundingClientRect()
        const workflow = rect('.workflow-links')
        const metrics = rect('.metric-grid')
        const panel = rect('.run-panel')
        const select = rect('.run-panel select')
        const input = rect('#match-limit')
        return { gap: metrics.top - workflow.bottom, left: input.left - panel.left,
          right: panel.right - input.right, alignment: Math.abs(select.left - input.left),
          overflow: document.documentElement.scrollWidth > innerWidth }
      })
      assert(spacing.gap >= 16 && spacing.left >= 20 && spacing.right >= 20)
      assert(spacing.alignment < 1 && !spacing.overflow)
      await page.getByText('最近 30 分钟没有已保留比赛', { exact: true }).waitFor()
      if (width >= 1024) {
        const trigger = page.getByRole('button', { name: '客户端更新', exact: true })
        await trigger.click()
        const dialog = page.getByRole('dialog')
        await dialog.waitFor()
        assert(await dialog.evaluate((element) => element.matches(':modal')))
        assert(await dialog.evaluate((element) => {
          const box = element.getBoundingClientRect()
          const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
          return element.contains(hit)
        }))
        for (let i = 0; i < 5; i++) {
          await page.keyboard.press('Tab')
          assert(await dialog.evaluate((element) => element.contains(document.activeElement)))
        }
        if (output) await page.screenshot({ path: path.join(output, `update-${width}.png`) })
        await page.keyboard.press('Escape')
        await dialog.waitFor({ state: 'hidden' })
        assert(await trigger.evaluate((element) => element === document.activeElement))
        await trigger.click()
        await dialog.getByRole('button', { name: '关闭', exact: true }).click()
        await dialog.waitFor({ state: 'hidden' })
      }
      if (output) await page.screenshot({ path: path.join(output, `dashboard-${width}.png`), fullPage: true })
      await page.goto(`${url}/tickets`)
      await page.getByRole('button', { name: '+ 新建 / 批量生成', exact: true }).click()
      await page.getByRole('button', { name: '批量与持续流量', exact: true }).click()
      await page.locator('.generator-card').first().waitFor()
      const composer = await page.evaluate(() => {
        const panel = document.querySelector('.composer-panel').getBoundingClientRect()
        const heading = document.querySelector('.composer-heading').getBoundingClientRect()
        const field = document.querySelector('.batch-panel .field-label').getBoundingClientRect()
        return { top: heading.top - panel.top, left: heading.left - panel.left,
          fieldLeft: field.left, headingLeft: heading.left, aligned: Math.abs(field.left - heading.left) < 1,
          overflow: document.documentElement.scrollWidth > innerWidth }
      })
      assert(composer.top >= 20 && composer.left >= 20 && composer.aligned && !composer.overflow, JSON.stringify(composer))
      if (output) await page.screenshot({ path: path.join(output, `tickets-${width}.png`), fullPage: true })
      console.log(`PASS ${width}px: panel spacing, form alignment, empty trend, ${width >= 1024 ? 'modal layering/focus/close' : 'narrow layout'}`)
      await page.close()
    }
  } finally { await browser.close() }
}
main().catch((error) => { console.error(error); process.exitCode = 1 })
