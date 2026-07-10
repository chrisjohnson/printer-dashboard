import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: 'html',
  use: {
    baseURL: process.env.SERVER_URL || 'http://localhost:8080',
    trace: 'on-first-retry',
  },
  ...(process.env.SERVER_URL
    ? {}
    : {
        webServer: {
          command:
            'go build -o printer-dashboard . && ./printer-dashboard tests/testdata/config.test.yaml',
          port: 8080,
          reuseExistingServer: true,
          timeout: 30000,
          stdout: 'pipe',
          stderr: 'pipe',
        },
      }),
});
