import { test, expect } from '@playwright/test';

test.describe('Dashboard', () => {
  test('page title is Printer Dashboard', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveTitle('Printer Dashboard');
  });

  test('shows printer count and add printer button', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('h1')).toContainText('Printer Dashboard');
    // Should show printer count when printers exist
    await expect(page.locator('#printer-count')).not.toBeEmpty();
    // Add Printer button should be visible
    await expect(page.locator('a.add-printer')).toContainText('+ Add Printer');
  });

  test('shows printer cards for all printers', async ({ page }) => {
    await page.goto('/');
    // Check that printer cards are rendered — should show Bambu + Snapmaker printers
    const cards = page.locator('.card');
    const count = await cards.count();
    expect(count).toBeGreaterThanOrEqual(1);
  });

  test('onboarding page loads and shows printer type selection', async ({
    page,
  }) => {
    await page.goto('/onboarding');
    await expect(page.locator('h1')).toContainText('+ Add Printer');
    await expect(page.locator('p.subtitle')).toHaveText(
      'Select your printer type to get started.',
    );
  });

  test('server has health endpoint', async ({ page }) => {
    const response = await page.request.get('/api/health');
    expect(response.status()).toBe(200);
    const body = await response.json();
    expect(body).toHaveProperty('status', 'ok');
  });

  test('api returns printers with camera streams', async ({ page }) => {
    const response = await page.request.get('/api/printers');
    expect(response.status()).toBe(200);
    const body = await response.json();
    expect(body).toHaveProperty('printers');
    expect(body.printers.length).toBeGreaterThanOrEqual(1);

    // Check that snapmaker printers have camera streams (screen/snapshot + touchscreen)
    const snapmakerPrinters = body.printers.filter(
      (p: any) => p.type === 'snapmaker',
    );
    for (const p of snapmakerPrinters) {
      expect(p.camera_streams).toBeDefined();
      expect(p.camera_streams.length).toBeGreaterThanOrEqual(1);
      // Touchscreen streams are NOT proxied — they are raw printer URLs
      const touchscreen = p.camera_streams.find(
        (c: any) => c.type === 'touchscreen',
      );
      if (touchscreen) {
        expect(touchscreen.url).toContain('/screen/snapshot');
        expect(touchscreen.url).not.toContain('/api/camera/proxy');
      }
      // Internal streams should be proxied
      const internal = p.camera_streams.find(
        (c: any) => c.type === 'internal',
      );
      if (internal) {
        expect(internal.url).toContain('/api/camera/proxy');
      }
    }
  });

  test('chamber temp row only shown for printers with a chamber heater', async ({
    page,
  }) => {
    await page.goto('/');
    // p1s has no chamber heater (no Model set → IsH2S("") is false).
    await expect(
      page.locator('#printer-p1s .temp-row[data-chamber]'),
    ).toHaveCount(0);
    // h2s has a chamber heater (Model: H2S).
    await expect(
      page.locator('#printer-h2s .temp-row[data-chamber]'),
    ).toHaveCount(1);
    // u1 (Snapmaker) never has a chamber heater.
    await expect(
      page.locator('#printer-u1 .temp-row[data-chamber]'),
    ).toHaveCount(0);
  });

  // K-080: toggleLight() must update window._printerCache optimistically,
  // not just the DOM, so a stale printer_update (carrying the pre-toggle
  // light state, as would happen if it raced an in-flight /light POST)
  // doesn't snap the toggle back before the command lands. Regression test
  // for the fix in onboarding.go's toggleLight().
  test('optimistic light toggle survives a racing stale WS update', async ({
    page,
  }) => {
    // Block the real WebSocket connection so genuine printer_update pushes
    // (these test printers cycle through connection-error states) can't
    // interfere with the deterministic race we're simulating below.
    await page.route('**/ws', (route) => route.abort());

    await page.goto('/');
    await expect(page.locator('#printer-u1 [data-light] .toggle input')).toBeAttached();

    // Hold the /light POST pending so we can inject a "stale" WS-style
    // update while the optimistic toggle's fetch is still in flight —
    // mirrors the real race between a WS push and a slower fetch response.
    let resolveLight: () => void;
    const lightPending = new Promise<void>((resolve) => {
      resolveLight = resolve;
    });
    await page.route('**/api/printers/u1/light', async (route) => {
      await lightPending;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ status: 'ok' }),
      });
    });

    const initiallyOn = await page.evaluate(
      () => (window as any)._printerCache['u1'].light_on === true,
    );

    // Click triggers toggleLight(), which optimistically flips the DOM and
    // (post-fix) window._printerCache — then the fetch above blocks. The
    // checkbox itself is visually hidden (opacity: 0, styled via .slider),
    // so click the visible slider — clicking the wrapping <label> forwards
    // the click to its associated input, same as a real user interaction.
    await page.locator('#printer-u1 [data-light] .toggle .slider').click();

    await expect(
      page.locator('#printer-u1 [data-light] .toggle input'),
    ).toBeChecked({ checked: !initiallyOn });
    await expect
      .poll(() =>
        page.evaluate(
          () => (window as any)._printerCache['u1'].light_on,
        ),
      )
      .toBe(!initiallyOn);

    // Simulate a stale printer_update arriving before the fetch resolves —
    // same shape connectWebSocket()'s onmessage handler processes, carrying
    // the printer's light state from before this toggle took effect.
    await page.evaluate((staleLightOn) => {
      const w = window as any;
      const merged = w.mergeWithCache({ id: 'u1', light_on: staleLightOn });
      w.updateCard(merged);
    }, initiallyOn);

    // The optimistic value must survive the stale update, in both the DOM
    // and the cache — this is the crux of K-080.
    await expect(
      page.locator('#printer-u1 [data-light] .toggle input'),
    ).toBeChecked({ checked: !initiallyOn });
    expect(
      await page.evaluate(() => (window as any)._printerCache['u1'].light_on),
    ).toBe(!initiallyOn);

    // Unblock the pending fetch and let it settle — the "pending" marker
    // toggleLight() sets should clear afterward, so the *next* WS update
    // (genuinely newer server state, not stale) is applied normally rather
    // than being suppressed forever.
    resolveLight!();
    await expect
      .poll(() =>
        page.evaluate(
          () => !!(window as any)._pendingFields?.['u1']?.light_on,
        ),
      )
      .toBe(false);

    await page.evaluate((newerLightOn) => {
      const w = window as any;
      const merged = w.mergeWithCache({ id: 'u1', light_on: newerLightOn });
      w.updateCard(merged);
    }, initiallyOn);

    // A real subsequent update is not suppressed — it reflects here even
    // though it happens to match the pre-toggle value (e.g. someone else
    // toggled the light back, or this is the driver's own eventual report).
    await expect(
      page.locator('#printer-u1 [data-light] .toggle input'),
    ).toBeChecked({ checked: initiallyOn });
    expect(
      await page.evaluate(() => (window as any)._printerCache['u1'].light_on),
    ).toBe(initiallyOn);
  });
});
