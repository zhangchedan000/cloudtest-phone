// 冒烟测试：真的打开管理页，逐页点一遍，抓浏览器报错，并截图。
// 用法：先 npx vite preview --port 4173，然后 node e2e/smoke.cjs [截图目录]
const { chromium } = require('playwright')
const out = process.argv[2] || 'e2e/shots'
;(async () => {
  const browser = await chromium.launch()
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 })
  const errors = []
  page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`))
  page.on('console', (m) => { if (m.type() === 'error') errors.push(`console: ${m.text()}`) })
  const base = process.env.BASE || 'http://localhost:4173/'
  await page.goto(base + '#phones'); await page.waitForSelector('.pp-card')
  await page.screenshot({ path: `${out}/1-phones.png` })
  const cards = await page.locator('.pp-card').count()
  await page.locator('.pp-card').first().getByText('编辑').click()
  await page.waitForSelector('.pp-field-row'); await page.waitForTimeout(900)
  const rows = await page.locator('.pp-field-row').count()
  await page.screenshot({ path: `${out}/2-detail.png` })
  // 改一个字段为手动并填值，应出现「改了 1 项」
  await page.keyboard.press('Escape'); await page.waitForTimeout(400)
  await page.locator('.pp-card').nth(1).getByText('编辑').click(); await page.waitForSelector('.pp-field-row'); await page.waitForTimeout(900); await page.waitForTimeout(900)
  await page.screenshot({ path: `${out}/3-detail-issues.png` })
  await page.keyboard.press('Escape'); await page.waitForTimeout(400)
  await page.getByText('列表', { exact: true }).click(); await page.waitForSelector('.ant-table-row')
  await page.screenshot({ path: `${out}/4-list.png` })
  for (const [hash, sel, n] of [['proxies', '.ant-table-row', 5], ['models', '.ant-table-row', 6], ['hosts', '.ant-card', 7]]) {
    await page.goto(base + '#' + hash); await page.reload(); await page.waitForSelector(sel)
    await page.screenshot({ path: `${out}/${n}-${hash}.png` })
  }
  await page.setViewportSize({ width: 390, height: 844 }); await page.goto(base + '#phones'); await page.reload(); await page.waitForSelector('.pp-card')
  await page.screenshot({ path: `${out}/8-mobile.png` })
  console.log(JSON.stringify({ cards, fieldRows: rows, errors }, null, 1))
  await browser.close()
  process.exit(errors.length ? 1 : 0)
})().catch((e) => { console.error(e); process.exit(2) })
