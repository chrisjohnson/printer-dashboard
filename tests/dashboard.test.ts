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

  // K-081: movement/homing control pad (jog X/Y/Z + Home All). u1 (Snapmaker)
  // is used throughout since it's reliably online+idle in this sandbox (the
  // Bambu test printers require real cloud credentials — see the chamber
  // temp row test above).
  test.describe('Movement pad', () => {
    test('jog and Home All buttons are disabled while printing, enabled while idle', async ({
      page,
    }) => {
      await page.goto('/');
      await expect(page.locator('#printer-u1 .jog-x-plus')).toBeAttached();

      // u1 starts idle in the test fixture — movement buttons should be enabled.
      await expect(page.locator('#printer-u1 .jog-x-plus')).toBeEnabled();
      await expect(page.locator('#printer-u1 .jog-z-plus')).toBeEnabled();
      await expect(page.locator('#printer-u1 .btn-home-all')).toBeEnabled();

      // Simulate a WS update putting the printer into "printing" — same
      // mergeWithCache()/updateCard() path a real printer_update message
      // takes (mirrors the optimistic-light-toggle test's WS simulation
      // above).
      await page.evaluate(() => {
        const w = window as any;
        const merged = w.mergeWithCache({ id: 'u1', state: 'printing' });
        w.updateCard(merged);
      });

      await expect(page.locator('#printer-u1 .jog-x-plus')).toBeDisabled();
      await expect(page.locator('#printer-u1 .jog-y-plus')).toBeDisabled();
      await expect(page.locator('#printer-u1 .jog-z-plus')).toBeDisabled();
      await expect(page.locator('#printer-u1 .btn-home-all')).toBeDisabled();

      // Switching back to idle re-enables them — this is the K-053-class
      // sync check: renderCard() and updateCard() must agree on which
      // buttons the .move-section disabled-toggle covers.
      await page.evaluate(() => {
        const w = window as any;
        const merged = w.mergeWithCache({ id: 'u1', state: 'idle' });
        w.updateCard(merged);
      });

      await expect(page.locator('#printer-u1 .jog-x-plus')).toBeEnabled();
      await expect(page.locator('#printer-u1 .btn-home-all')).toBeEnabled();
    });

    test('X/Y jog buttons send the jog request immediately with no confirmation', async ({
      page,
    }) => {
      let jogBody: any = null;
      await page.route('**/api/printers/u1/jog', async (route) => {
        jogBody = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ status: 'ok' }),
        });
      });

      await page.goto('/');
      await expect(page.locator('#printer-u1 .jog-x-plus')).toBeEnabled();

      // Default step size is the smallest/most conservative option (0.1mm).
      await expect(page.locator('#printer-u1 .step-select')).toHaveValue('0.1');

      // No confirmation dialog should appear for X/Y — fail the test if one does.
      page.once('dialog', (dialog) => {
        throw new Error('Unexpected dialog for X jog: ' + dialog.message());
      });

      await page.locator('#printer-u1 .jog-x-plus').click();

      await expect.poll(() => jogBody).not.toBeNull();
      expect(jogBody).toEqual({ x: 0.1, y: 0, z: 0 });

      // Y- with a larger step size selected.
      jogBody = null;
      await page.locator('#printer-u1 .step-select').selectOption('10');
      await page.locator('#printer-u1 .jog-y-minus').click();

      await expect.poll(() => jogBody).not.toBeNull();
      expect(jogBody).toEqual({ x: 0, y: -10, z: 0 });
    });

    test('Z jog button shows a confirmation dialog and only sends after confirming', async ({
      page,
    }) => {
      let jogBody: any = null;
      await page.route('**/api/printers/u1/jog', async (route) => {
        jogBody = route.request().postDataJSON();
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ status: 'ok' }),
        });
      });

      await page.goto('/');
      await expect(page.locator('#printer-u1 .jog-z-plus')).toBeEnabled();

      // Clicking Z+ shows the in-page confirmation modal (not a native
      // dialog), and must NOT send the request yet.
      await page.locator('#printer-u1 .jog-z-plus').click();
      await expect(page.locator('#zjog-modal-u1')).toBeVisible();
      await expect(page.locator('#zjog-modal-text-u1')).toContainText('Move Z by 0.1mm');
      expect(jogBody).toBeNull();

      // Cancel — must not send anything.
      await page.locator('#zjog-modal-u1 .btn-zjog-cancel').click();
      await expect(page.locator('#zjog-modal-u1')).toBeHidden();
      expect(jogBody).toBeNull();

      // Click Z+ again and confirm this time.
      await page.locator('#printer-u1 .jog-z-plus').click();
      await expect(page.locator('#zjog-modal-u1')).toBeVisible();
      await page.locator('#zjog-modal-u1 .btn-zjog-confirm').click();
      await expect(page.locator('#zjog-modal-u1')).toBeHidden();

      await expect.poll(() => jogBody).not.toBeNull();
      expect(jogBody).toEqual({ x: 0, y: 0, z: 0.1 });
    });

    test('Home All sends the home request with no confirmation', async ({
      page,
    }) => {
      let homeCalled = false;
      await page.route('**/api/printers/u1/home', async (route) => {
        homeCalled = true;
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ status: 'ok' }),
        });
      });

      await page.goto('/');
      await expect(page.locator('#printer-u1 .btn-home-all')).toBeEnabled();

      page.once('dialog', (dialog) => {
        throw new Error('Unexpected dialog for Home All: ' + dialog.message());
      });

      await page.locator('#printer-u1 .btn-home-all').click();

      await expect.poll(() => homeCalled).toBe(true);
    });
  });

  // K-069: a live printer_update must never clobber a user's in-progress text
  // selection inside a .val span (e.g. selecting a temperature to copy it),
  // and should skip the DOM write entirely when the value hasn't actually
  // changed (the common case — updates arrive every 1-3s regardless of
  // whether the temp moved). Regression test for setValText(), the helper
  // used at all four .val write sites in updateCard()'s temperature block.
  //
  // This exercises setValText() directly (a plain global, like the other
  // onboarding.go helpers — see mergeWithCache() usage in the K-080 test
  // above) against a real .val span and the real Selection API, in isolation
  // from the rest of the update pipeline. Note that setValText() alone was
  // NOT sufficient to fix the user-reported bug: updateCard() unconditionally
  // calls reorderCard() at the end, and reorderCard() used to unconditionally
  // call insertBefore()/appendChild() to place the card — even a "no-op"
  // reorder detaches and reattaches the node, which collapses any live
  // selection inside it regardless of setValText()'s own guard. See the
  // "a full printer_update through updateCard()..." test below, which
  // exercises the real mergeWithCache()->updateCard()->reorderCard() pipeline
  // end-to-end (this test deliberately does not, to isolate setValText()).
  test('setValText skips unchanged writes and does not clobber an active selection', async ({
    page,
  }) => {
    await page.goto('/');
    const bedVal = page.locator('#printer-u1 .temp-row .val').first();
    await expect(bedVal).toBeAttached();
    const originalText = await bedVal.textContent();
    expect(originalText).toBeTruthy();

    // Programmatically select all the text inside the .val span, mirroring
    // a user selecting the value to copy it.
    await page.evaluate(() => {
      const el = document.querySelector('#printer-u1 .temp-row .val')!;
      const range = document.createRange();
      range.selectNodeContents(el);
      const sel = window.getSelection()!;
      sel.removeAllRanges();
      sel.addRange(range);
    });

    const isSelected = async () =>
      page.evaluate(() => {
        const el = document.querySelector('#printer-u1 .temp-row .val');
        const sel = window.getSelection();
        return (
          !!sel &&
          !sel.isCollapsed &&
          !!el &&
          el.contains(sel.anchorNode) &&
          sel.toString().length > 0
        );
      });
    expect(await isSelected()).toBe(true);

    // 1. Call setValText() with the SAME text — should no-op on the
    // unchanged-text check before it even looks at the selection, but
    // either guard suffices here.
    await page.evaluate((text) => {
      const w = window as any;
      const el = document.querySelector('#printer-u1 .temp-row .val');
      w.setValText(el, text);
    }, originalText);

    expect(await bedVal.textContent()).toBe(originalText);
    expect(await isSelected()).toBe(true);

    // 2. Call setValText() with DIFFERENT text while the selection is still
    // active. Per the card's spec, setValText() must not clobber an active
    // selection even when the value genuinely changed — the display write
    // is deferred until the user clears their selection, same tradeoff
    // setTargetInput() already makes for a focused input.
    const differentText = '999.9°C';
    await page.evaluate((text) => {
      const w = window as any;
      const el = document.querySelector('#printer-u1 .temp-row .val');
      w.setValText(el, text);
    }, differentText);

    expect(await bedVal.textContent()).toBe(originalText);
    expect(await isSelected()).toBe(true);

    // 3. Clear the selection and call setValText() with the same changed
    // text again — the guard must only defer the write, not permanently
    // suppress it.
    await page.evaluate(() => window.getSelection()!.removeAllRanges());
    await page.evaluate((text) => {
      const w = window as any;
      const el = document.querySelector('#printer-u1 .temp-row .val');
      w.setValText(el, text);
    }, differentText);

    await expect(bedVal).toHaveText(differentText);
  });

  // K-069 follow-up: setValText() alone is not enough. updateCard() (the
  // real handler for every WS printer_update, ~1-3s cadence) unconditionally
  // calls reorderCard() at the end, and reorderCard() used to unconditionally
  // call insertBefore()/appendChild() to place the card — even when the
  // card's sort position hadn't actually changed. Per the DOM spec, moving a
  // node (detach + reinsert) collapses any active Selection anchored inside
  // it, regardless of setValText()'s own guard. This test drives a
  // printer_update through the REAL mergeWithCache()/updateCard() pipeline
  // (unlike the test above, which calls setValText() directly) to prove the
  // end-to-end path — setValText()'s no-op write guard plus reorderCard()'s
  // no-op move guard together — no longer clobbers the user's selection.
  test('a full printer_update through updateCard() does not clobber an active selection when sort position is unchanged', async ({
    page,
  }) => {
    // Block the real WebSocket so genuine printer_update pushes (these test
    // printers cycle through connection-error states) can't race the
    // deterministic update simulated below — same precaution as the K-080 test.
    await page.route('**/ws', (route) => route.abort());

    await page.goto('/');
    const bedVal = page.locator('#printer-u1 .temp-row .val').first();
    await expect(bedVal).toBeAttached();
    const originalText = await bedVal.textContent();
    expect(originalText).toBeTruthy();

    const originalOrder = await page.evaluate(() =>
      Array.from(document.querySelectorAll('#printer-list .card')).map(
        (c) => c.id,
      ),
    );

    // Select the text inside the .val span, mirroring a user copying it.
    await page.evaluate(() => {
      const el = document.querySelector('#printer-u1 .temp-row .val')!;
      const range = document.createRange();
      range.selectNodeContents(el);
      const sel = window.getSelection()!;
      sel.removeAllRanges();
      sel.addRange(range);
    });

    const isSelected = async () =>
      page.evaluate(() => {
        const el = document.querySelector('#printer-u1 .temp-row .val');
        const sel = window.getSelection();
        return (
          !!sel &&
          !sel.isCollapsed &&
          !!el &&
          el.contains(sel.anchorNode) &&
          sel.toString().length > 0
        );
      });
    expect(await isSelected()).toBe(true);

    // Push a printer_update carrying the SAME bed_temp (parsed back out of
    // the rendered text) and no other fields — mergeWithCache() layers it
    // onto the cached printer, so state/name (and therefore reorderCard()'s
    // sort position) are untouched, exactly matching the common case of a
    // WS push arriving with no actual change. This goes through the real
    // mergeWithCache()->updateCard() pipeline, including its trailing
    // reorderCard(p.id) call — the exact path the prior setValText()-only
    // test above deliberately avoided.
    await page.evaluate(() => {
      const w = window as any;
      const cached = w._printerCache['u1'];
      const merged = w.mergeWithCache({ id: 'u1', bed_temp: cached.bed_temp });
      w.updateCard(merged);
    });

    // The selection must survive the full pipeline, and the card must stay
    // in the same position (proving reorderCard() genuinely no-opped rather
    // than "coincidentally" landing in the same place).
    expect(await bedVal.textContent()).toBe(originalText);
    expect(await isSelected()).toBe(true);
    const orderAfter = await page.evaluate(() =>
      Array.from(document.querySelectorAll('#printer-list .card')).map(
        (c) => c.id,
      ),
    );
    expect(orderAfter).toEqual(originalOrder);
  });

  // Companion to the no-op reorder test above: make sure the "skip if
  // already correct" guard added to reorderCard() doesn't also suppress a
  // GENUINE reorder. The Bambu-backed test printers (p1s/h2s) don't render
  // in this sandbox (they require real Bambu cloud auth — same pre-existing
  // gap as the "chamber temp row" test above), so this injects a second,
  // synthetic card via the real renderCard() helper (a plain global, like
  // the other onboarding.go helpers used throughout this file) rather than
  // depending on Bambu connectivity. Named so it alphabetically sorts after
  // "Snapmaker U1" while idle, then forced into the 'error' tier (which
  // sorts first, per reorderCard()'s sortPriority()) to prove it actually
  // moves to the front of #printer-list.
  test('reorderCard still moves a card when its sort position genuinely changes', async ({
    page,
  }) => {
    await page.route('**/ws', (route) => route.abort());
    await page.goto('/');
    await expect(page.locator('#printer-u1')).toBeAttached();

    await page.evaluate(() => {
      const w = window as any;
      const synthetic = {
        id: 'zz-synthetic',
        name: 'ZZ Synthetic Printer',
        state: 'idle',
        online: true,
        progress: 0,
        remaining_time: 0,
        current_file: null,
        current_layer: 0,
        total_layers: 0,
        bed_temp: 20,
        bed_target_temp: null,
        nozzle_temp: 20,
        nozzle_target_temp: null,
        chamber_temp: null,
        chamber_target_temp: null,
        hms_errors: [],
        hms_warnings: [],
      };
      w._printerCache['zz-synthetic'] = synthetic;
      document
        .getElementById('printer-list')!
        .insertAdjacentHTML('beforeend', w.renderCard(synthetic));
    });
    await expect(page.locator('#printer-zz-synthetic')).toBeAttached();

    const orderBefore = await page.evaluate(() =>
      Array.from(document.querySelectorAll('#printer-list .card')).map(
        (c) => c.id,
      ),
    );
    // Idle sorts last (sortPriority() returns 2 for 'idle'), and "ZZ..."
    // sorts after "Snapmaker U1" alphabetically either way, so the synthetic
    // card must already be last — otherwise forcing it to 'error' below
    // wouldn't prove a move.
    expect(orderBefore[orderBefore.length - 1]).toBe('printer-zz-synthetic');

    await page.evaluate(() => {
      const w = window as any;
      const merged = w.mergeWithCache({ id: 'zz-synthetic', state: 'error' });
      w.updateCard(merged);
    });

    await expect(page.locator('#printer-zz-synthetic .tag')).toHaveText(
      'error',
    );
    const orderAfter = await page.evaluate(() =>
      Array.from(document.querySelectorAll('#printer-list .card')).map(
        (c) => c.id,
      ),
    );
    // Error tier sorts first (sortPriority() returns 0 for 'error'), so the
    // synthetic card must now be the first card in the DOM.
    expect(orderAfter[0]).toBe('printer-zz-synthetic');
    expect(orderAfter).not.toEqual(orderBefore);
  });
});
