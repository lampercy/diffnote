const { test, expect } = require('@playwright/test');

async function setup(page) {
  await page.route('**/api/**', async route => {
    const url = new URL(route.request().url());
    let body = {};
    if (url.pathname === '/api/projects') body = [];
    if (url.pathname === '/api/state') body = { repository: null };
    await route.fulfill({ json: body });
  });
  await page.goto('/');
  await expect(page.locator('#diff-root')).toContainText('Add a project');
}

async function fixture(page, count = 4000, sparse = false) {
  await page.evaluate(({ count, sparse }) => {
    const hunks = [];
    for (let n = 1; n <= count; n += sparse ? 100 : count) {
      const size = sparse ? 8 : count;
      hunks.push({ header: `@@ -${n},${size} +${n},${size} @@`, lines: Array.from({ length: size }, (_, i) => ({ kind: 'context', oldLine: n + i, newLine: n + i, content: ` const value${n + i} = ${n + i};` })) });
    }
    state.commit = { hash: 'abcd1234', subject: 'Fixture', files: [{ oldPath: 'large.js', newPath: 'large.js', status: 'modified', hunks }] };
    state.comments = [];
    state.orphans = [];
    disposeVirtualDiff();
    state.virtualDiff = null;
    renderDiff();
  }, { count, sparse });
  await settle(page);
}

async function settle(page) {
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => requestAnimationFrame(resolve)))));
}

async function scroll(page, top) {
  await page.locator('.review-pane').evaluate((el, top) => { el.scrollTop = top; }, top);
  await settle(page);
}

async function visibleAnchor(page) {
  return page.evaluate(() => {
    const top = elements.reviewPane.getBoundingClientRect().top + 140;
    const row = [...document.querySelectorAll('.line-row')].find(row => row.getBoundingClientRect().top >= top);
    return { key: row.dataset.anchor, top: row.getBoundingClientRect().top };
  });
}

async function anchorTop(page, key) {
  return page.evaluate(key => [...document.querySelectorAll('.line-row')].find(row => row.dataset.anchor === key)?.getBoundingClientRect().top, key);
}

test.beforeEach(async ({ page }) => setup(page));

test('expanding deep context preserves the exact line and viewport offset', async ({ page }) => {
  await fixture(page, 8000, true);
  await scroll(page, 12000);
  const target = await page.evaluate(() => {
    const button = [...document.querySelectorAll('.expand-context')].find(el => el.getBoundingClientRect().top > 230);
    const row = nextAnchorRow(button.closest('tr'));
    return { key: row.dataset.anchor, top: row.getBoundingClientRect().top };
  });
  await page.route('**/api/commits/abcd1234?context=13', async route => {
    const commit = await page.evaluate(() => {
      const commit = structuredClone(state.commit);
      for (const hunk of commit.files[0].hunks) {
        const start = hunk.lines[0].newLine;
        if (start === 1) continue;
        const lines = Array.from({ length: 10 }, (_, i) => ({ kind: 'context', oldLine: start - 10 + i, newLine: start - 10 + i, content: ` context ${start - 10 + i}` }));
        hunk.lines.unshift(...lines);
        hunk.header = `@@ -${start - 10},18 +${start - 10},18 @@`;
      }
      return commit;
    });
    await route.fulfill({ json: commit });
  });
  await page.evaluate(async key => {
    const row = [...document.querySelectorAll('.line-row')].find(row => row.dataset.anchor === key);
    await expandContext(row.previousElementSibling.querySelector('button'));
  }, target.key);
  await settle(page);
  expect(await anchorTop(page, target.key)).toBeCloseTo(target.top, 0);
});

test('comment creation, typing, deletion and virtual scrolling retain position and focus', async ({ page }) => {
  await fixture(page);
  await scroll(page, 9000);
  const anchor = await visibleAnchor(page);
  await page.evaluate(key => addComment(JSON.parse(key).reduce((a, v, i) => ({ ...a, [['filePath', 'side', 'line', 'context'][i]]: v }), {})), anchor.key);
  await settle(page);
  expect(await anchorTop(page, anchor.key)).toBeCloseTo(anchor.top, 0);
  await page.locator('.comment-row textarea').fill('Regression feedback');
  const position = await page.locator('.review-pane').evaluate(el => el.scrollTop);
  await scroll(page, position + 500);
  await expect(page.locator('.comment-row textarea')).toBeFocused();
  const beforeDelete = await visibleAnchor(page);
  await page.locator('.delete-comment').evaluate(el => el.click());
  await settle(page);
  expect(await anchorTop(page, beforeDelete.key)).toBeCloseTo(beforeDelete.top, 0);
});

test('history pagination preserves the visible card and scroll offset', async ({ page }) => {
  await page.evaluate(() => {
    state.commits = Array.from({ length: 100 }, (_, i) => ({ hash: `hash${i}`, shortHash: `${i}`, subject: `Commit ${i}`, date: new Date().toISOString(), parents: [] }));
    state.commitTotal = 200;
    state.commitTip = 'tip';
    renderCommitList();
    elements.commitList.scrollTop = 2000;
  });
  await page.route('**/api/commits?tip=**', route => route.fulfill({ json: { commits: Array.from({ length: 100 }, (_, i) => ({ hash: `next${i}`, shortHash: `${i}`, subject: `Next ${i}`, date: new Date().toISOString(), parents: [] })), total: 200 } }));
  const before = await page.locator('#commit-list').evaluate(el => el.scrollTop);
  const cardBefore = await page.locator('#commit-list').evaluate(el => {
    const card = [...el.children].find(card => card.getBoundingClientRect().top >= el.getBoundingClientRect().top);
    return { hash: card.dataset.hash, top: card.getBoundingClientRect().top };
  });
  await page.evaluate(() => loadMoreCommits());
  expect(await page.locator('#commit-list').evaluate(el => el.scrollTop)).toBe(before);
  expect(await page.locator(`[data-hash="${cardBefore.hash}"]`).evaluate(el => el.getBoundingClientRect().top)).toBe(cardBefore.top);
  await expect(page.locator('.commit-card')).toHaveCount(200);
});

test('virtual block boundaries have no extra separators or missing source lines', async ({ page }) => {
  await fixture(page, 125);
  await scroll(page, 800);
  await page.evaluate(() => {
    const blocks = [...document.querySelectorAll('.virtual-block')];
    for (let i = 1; i < blocks.length; i++) {
      const gap = blocks[i].getBoundingClientRect().top - blocks[i - 1].getBoundingClientRect().bottom;
      if (gap !== 0) throw new Error(`Unexpected separator gap: ${gap}`);
    }
    const lines = [...document.querySelectorAll('.line-row')].map(row => JSON.parse(row.dataset.anchor)[2]);
    for (let i = 1; i < lines.length; i++) if (lines[i] !== lines[i - 1] + 1) throw new Error('Missing or duplicated line');
    if (document.querySelectorAll('.hunk-row').length > 1) throw new Error('Extra hunk separators');
  });
});

test('large diffs keep DOM, observers and retained heap bounded across repeated scrolling', async ({ page }) => {
  await fixture(page, 100000);
  const session = await page.context().newCDPSession(page);
  await session.send('HeapProfiler.collectGarbage');
  const before = await session.send('Memory.getDOMCounters');
  const heapBefore = (await session.send('Runtime.getHeapUsage')).usedSize;
  for (let i = 0; i < 70; i++) {
    await scroll(page, (i % 35) * 14000);
    expect(await page.locator('.line-row').count()).toBeLessThan(250);
  }
  await scroll(page, 0);
  await session.send('HeapProfiler.collectGarbage');
  const after = await session.send('Memory.getDOMCounters');
  const heapAfter = (await session.send('Runtime.getHeapUsage')).usedSize;
  console.log(JSON.stringify({ before, after, heapBefore, heapAfter }));
  expect(after.nodes - before.nodes).toBeLessThan(5000);
  expect(heapAfter - heapBefore).toBeLessThan(12 * 1024 * 1024);
});

test('rerenders disconnect old observers and do not retain old commits', async ({ page }) => {
  await page.evaluate(() => {
    const NativeObserver = ResizeObserver;
    window.observedTargets = new Set();
    window.ResizeObserver = class extends NativeObserver {
      constructor(callback) { super(callback); this.targets = new Set(); }
      observe(target) { this.targets.add(target); observedTargets.add(target); super.observe(target); }
      unobserve(target) { this.targets.delete(target); observedTargets.delete(target); super.unobserve(target); }
      disconnect() { for (const target of this.targets) observedTargets.delete(target); this.targets.clear(); super.disconnect(); }
    };
  });
  await fixture(page, 100000);
  for (let i = 0; i < 15; i++) {
    await scroll(page, i * 15000);
    await page.evaluate(() => renderDiff());
    await settle(page);
    expect(await page.evaluate(() => observedTargets.size)).toBe(await page.locator('.virtual-block').count());
  }
  await fixture(page, 100);
  expect(await page.evaluate(() => observedTargets.size)).toBe(await page.locator('.virtual-block').count());
});

test('wrapped lines and resizing a comment do not move the source anchor', async ({ page }) => {
  await fixture(page, 2000);
  await page.evaluate(() => {
    for (const line of state.commit.files[0].hunks[0].lines) line.content = ` const value = "${'long source text '.repeat(15)}";`;
    renderDiff();
  });
  await scroll(page, 16000);
  const anchor = await visibleAnchor(page);
  await page.evaluate(key => {
    const [filePath, side, line, context] = JSON.parse(key);
    addComment({ filePath, side, line, context });
  }, anchor.key);
  await settle(page);
  expect(await anchorTop(page, anchor.key)).toBeCloseTo(anchor.top, 0);
  await page.locator('.comment-row textarea').evaluate(el => { el.style.height = '260px'; });
  await settle(page);
  expect(await anchorTop(page, anchor.key)).toBeCloseTo(anchor.top, 0);
  await expect(page.locator('.comment-row textarea')).toBeFocused();
});
