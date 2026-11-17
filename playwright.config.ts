import { defineConfig } from '@playwright/test';

// The suite runs against a self-contained fleet: tests/run-test-server.sh
// starts the stub Snapmaker on 127.0.0.1:18081 and then the dashboard on
// 127.0.0.1:18080 with tests/testdata/config.test.yaml. No real printer is
// involved, so the result does not depend on what the fleet is doing.
//
// SERVER_URL still overrides everything for pointing the specs at an already
// running instance (`SERVER_URL=... npx playwright test`).
const testPort = 18080;

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: 'html',
  use: {
    baseURL: process.env.SERVER_URL || `http://127.0.0.1:${testPort}`,
    trace: 'on-first-retry',
  },
  ...(process.env.SERVER_URL
    ? {}
    : {
        webServer: {
          command: 'tests/run-test-server.sh',
          port: testPort,
          // Deliberately false. With reuseExistingServer the suite happily
          // attached to whatever was already on the port — historically a dev
          // instance on :8080 pointed at the live printers — and reported green
          // while testing the wrong server. Fail loudly instead.
          reuseExistingServer: false,
          timeout: 60000,
          stdout: 'pipe',
          stderr: 'pipe',
        },
      }),
});
