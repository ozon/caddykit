import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: './e2e',
	timeout: 30_000,
	retries: 0,
	reporter: 'list',
	use: {
		baseURL: process.env.CADDYKIT_E2E_BASE_URL,
		trace: 'retain-on-failure'
	}
});
