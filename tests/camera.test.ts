import { test, expect } from '@playwright/test';

test.describe('Camera Stream Proxy', () => {
  test('proxy rejects missing url parameter with 400', async ({ page }) => {
    const response = await page.request.get('/api/camera/proxy');
    expect(response.status()).toBe(400);
    const body = await response.json();
    expect(body).toEqual({ error: 'missing or invalid url parameter' });
  });

  test('proxy rejects relative url with 400', async ({ page }) => {
    const response = await page.request.get(
      '/api/camera/proxy?url=/relative/path',
    );
    expect(response.status()).toBe(400);
    const body = await response.json();
    expect(body).toEqual({ error: 'missing or invalid url parameter' });
  });

  test('proxy rejects invalid url with 400', async ({ page }) => {
    const response = await page.request.get('/api/camera/proxy?url=not-a-url');
    expect(response.status()).toBe(400);
    const body = await response.json();
    expect(body).toEqual({ error: 'missing or invalid url parameter' });
  });

  test('proxy returns 502 for unreachable upstream', async ({ page }) => {
    const response = await page.request.get(
      '/api/camera/proxy?url=http://127.0.0.1:1/nonexistent',
    );
    expect(response.status()).toBe(502);
    const body = await response.json();
    expect(body).toEqual({ error: 'camera stream unreachable' });
  });

  test('proxy serves snapmaker touchscreen snapshot', async ({ page }) => {
    // The U1 snapmaker has a /screen/snapshot endpoint that returns PNG
    const response = await page.request.get(
      '/api/camera/proxy?url=http%3A%2F%2F192.168.1.10%3A80%2Fscreen%2Fsnapshot',
    );
    expect(response.status()).toBe(200);
    const ct = response.headers()['content-type'] || '';
    expect(ct).toBe('image/png');
  });

  test('dashboard renders camera section with single slot that flips between feeds', async ({
    page,
  }) => {
    await page.goto('/');
    const cameraSection = page.locator('#cam-section-u1');
    await expect(cameraSection).toBeVisible();

    // Single slot with a camera-nav for flipping between streams
    const nav = cameraSection.locator('.camera-nav');
    await expect(nav).toBeVisible();

    // Initially shows an img (Camera stream — MJPEG)
    const img = cameraSection.locator('img');
    await expect(img).toBeVisible();

    // Click "next" to flip to touchscreen stream
    const nextBtn = cameraSection.locator('.cam-next');
    await nextBtn.click();
    await page.waitForTimeout(300); // allow re-render

    // Should now show an img.touchscreen-img (Touchscreen stream)
    const touchscreenImg = cameraSection.locator('img.touchscreen-img');
    await expect(touchscreenImg).toBeVisible({ timeout: 5000 });

    // Click "prev" to flip back to camera stream
    const prevBtn = cameraSection.locator('.cam-prev');
    await prevBtn.click();
    await page.waitForTimeout(300);

    // Should now show an img again
    await expect(img).toBeVisible();
  });
});
