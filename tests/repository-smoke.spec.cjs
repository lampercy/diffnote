const { test, expect } = require('@playwright/test');

// Opt-in: uses a separately started DiffNote with an isolated database. Never
// checks out, edits, or commits anything in the reviewed repository.
test('large real repository commits, context, comments, pagination and heap', async ({ page }) => {
  test.skip(!process.env.DIFFNOTE_SMOKE_URL || !process.env.DIFFNOTE_SMOKE_REPO, 'Set DIFFNOTE_SMOKE_URL and DIFFNOTE_SMOKE_REPO to run the real-repository smoke test');
  test.setTimeout(180_000);
  const base = process.env.DIFFNOTE_SMOKE_URL;
  const response = await page.request.post(`${base}/api/projects`, { data: { path: process.env.DIFFNOTE_SMOKE_REPO } });
  expect(response.ok()).toBeTruthy();
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto(base);
  await expect(page.locator('.virtual-block').first()).toBeVisible();
  const session = await page.context().newCDPSession(page);
  const commits = (process.env.DIFFNOTE_SMOKE_COMMITS || '8e767be00c4b5745fd16df621f0e6e9f97f751bc,f0f10796530b14f3bcde6a883ed515c13da5a1d5,424bbc925eb786faf9c3762790af090c5285b787,3659318a3f56503c4abc55855d794ac1d358cc36,7dbc99b3f536feb6faf6ccd42017bd0883d262b7').split(',');
  const measurements = [];
  for (const hash of commits) {
    await page.evaluate(hash => selectCommit(hash), hash);
    await expect(page.locator('.virtual-block').first()).toBeAttached();
    const count = await page.evaluate(() => state.commit.files.reduce((n, file) => n + file.hunks.reduce((n, hunk) => n + hunk.lines.length, 0), 0));
    for (let i = 0; i <= 12; i++) {
      await page.evaluate(fraction => { elements.reviewPane.scrollTop = fraction * (elements.reviewPane.scrollHeight - elements.reviewPane.clientHeight); }, i / 12);
      await page.waitForTimeout(40);
      expect(await page.locator('.line-row').count()).toBeLessThan(400);
      expect(await page.locator('.line-row.meta').filter({ hasText: /^\s*$/ }).count()).toBe(0);
    }
    await session.send('HeapProfiler.collectGarbage');
    const heap = (await session.send('Runtime.getHeapUsage')).usedSize;
    const dom = await session.send('Memory.getDOMCounters');
    measurements.push({ hash, lines: count, heap, nodes: dom.nodes });
    expect(heap).toBeLessThan(150 * 1024 * 1024);
    expect(dom.nodes).toBeLessThan(40000);
    // Exercise expansion at a distant hunk, not just the first file.
    const expanded = await page.evaluate(async () => {
      const button = [...document.querySelectorAll('.expand-context')].find(el => el.getBoundingClientRect().top > 200 && el.getBoundingClientRect().top < 700);
      if (!button) return false;
      const row = nextAnchorRow(button.closest('tr'));
      const key = row.dataset.anchor;
      const top = row.getBoundingClientRect().top;
      await expandContext(button);
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const restored = [...document.querySelectorAll('.line-row')].find(row => row.dataset.anchor === key);
      if (!restored || Math.abs(restored.getBoundingClientRect().top - top) > 1) throw new Error('Real context expansion moved source line');
      return true;
    });
    measurements.at(-1).expanded = expanded;
  }
  // Scroll the sidebar to load a real second page without moving the diff.
  const prior = await page.evaluate(() => ({ count: state.commits.length, top: elements.reviewPane.scrollTop }));
  await page.evaluate(() => { elements.commitList.scrollTop = elements.commitList.scrollHeight; });
  await expect.poll(() => page.evaluate(() => state.commits.length)).toBeGreaterThan(prior.count);
  expect(await page.evaluate(() => elements.reviewPane.scrollTop)).toBe(prior.top);
  // Save and reload a comment against the isolated review database.
  await page.evaluate(() => {
    const row = [...document.querySelectorAll('.line-row[data-anchor]')].find(el => el.getBoundingClientRect().top > 200);
    const [filePath, side, line, context] = JSON.parse(row.dataset.anchor);
    addComment({ filePath, side, line, context });
  });
  await page.locator('.comment-row textarea').fill('Isolated smoke-test feedback');
  await page.evaluate(() => saveComments(false));
  const commentID = await page.evaluate(() => state.comments.at(-1).id);
  const hash = await page.evaluate(() => state.commit.hash);
  await page.evaluate(hash => selectCommit(hash), hash);
  expect(await page.evaluate(() => state.comments.some(comment => comment.body === 'Isolated smoke-test feedback'))).toBeTruthy();
  await page.evaluate(async id => {
    state.comments = state.comments.filter(comment => comment.id !== id);
    await saveComments(false);
  }, commentID);
  expect(errors).toEqual([]);
  console.log('Repository smoke measurements:', JSON.stringify(measurements));
});
