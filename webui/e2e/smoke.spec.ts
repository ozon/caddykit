import { test, expect } from '@playwright/test';

// These tests need a running devproxy web process and the start password.
// Example:
//   DEVPROXY_E2E_BASE_URL=http://localhost:<backend-port> \
//   DEVPROXY_E2E_PASSWORD=<start password> npm run test:e2e
const base = process.env.DEVPROXY_E2E_BASE_URL;
const password = process.env.DEVPROXY_E2E_PASSWORD;

test.skip(!base || !password, 'set DEVPROXY_E2E_BASE_URL and DEVPROXY_E2E_PASSWORD');

test('login, dashboard, live preview, conflicts, undo', async ({ page }) => {
	await page.goto('/login');
	await page.locator('#password').fill(password!);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('heading', { name: 'Routes' })).toBeVisible();

	// Command palette opens with the keyboard.
	await page.keyboard.press(process.platform === 'darwin' ? 'Meta+k' : 'Control+k');
	await expect(page.getByPlaceholder('Jump to a route or run an action…')).toBeVisible();
	await page.keyboard.press('Escape');

	// Live preview on the create form.
	await page.getByRole('button', { name: 'New route' }).click();
	await page.locator('#host').fill('e2e.example.com');
	await page.locator('#upstream').fill('127.0.0.1:3999');
	await expect(page.locator('.diff-row').first()).toBeVisible({ timeout: 10_000 });

	// The JSON editor is lazy-loaded only on its tab (checked via detail page
	// when a route exists; here we only assert the form preview).
	await page.getByRole('link', { name: 'All routes' }).click();
	await expect(page.getByRole('heading', { name: 'Routes' })).toBeVisible();
});

test('conflict center and undo are reachable', async ({ page }) => {
	await page.goto('/login');
	await page.locator('#password').fill(password!);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await page.goto('/conflicts');
	await expect(page.getByRole('heading', { name: 'Conflict center' })).toBeVisible();
	await page.goto('/undo');
	await expect(page.getByRole('heading', { name: 'Undo' })).toBeVisible();
});
