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
});
