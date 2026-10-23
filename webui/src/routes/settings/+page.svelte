<script lang="ts">
	import { api, formatTime } from '$lib/api';
	import { app, setTheme, toast } from '$lib/store.svelte';
	import { Check, Copy, KeyRound, Moon, ShieldAlert, Sun } from '@lucide/svelte';

	let newPassword = $state('');
	let rotating = $state(false);
	let copied = $state(false);

	async function rotate() {
		rotating = true;
		try {
			const res = await api<{ password: string }>('/api/v1/auth/rotate-password', {
				method: 'POST',
				body: '{}'
			});
			newPassword = res.password;
			toast('Password rotated — other sessions were invalidated');
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Rotation failed');
		} finally {
			rotating = false;
		}
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(newPassword);
			copied = true;
			window.setTimeout(() => (copied = false), 1500);
		} catch {
			toast('Clipboard unavailable');
		}
	}
</script>

<svelte:head><title>Settings · caddykit</title></svelte:head>

<div class="mb-4">
	<h1 class="text-[18px] font-semibold tracking-tight">Settings</h1>
	<p class="muted mt-0.5 text-[12px]">Process-local. Nothing here persists across a restart except the browser theme.</p>
</div>

<div class="grid max-w-[980px] gap-3 md:grid-cols-2">
	<section class="panel p-4">
		<h2 class="text-[13.5px] font-semibold">Instance</h2>
		<dl class="mono mt-3 grid grid-cols-[130px_1fr] gap-x-3 gap-y-2 text-[11.5px]">
			<dt class="faint">domain</dt><dd>{app.status?.domain ?? '—'}</dd>
			<dt class="faint">version</dt><dd>{app.status?.version ?? '—'}</dd>
			<dt class="faint">server</dt><dd>{app.status?.server ?? '—'}</dd>
			<dt class="faint">reconcile</dt><dd>{app.status?.reconcile_interval ?? '—'}</dd>
			<dt class="faint">last sync</dt><dd>{formatTime(app.status?.last_reconcile)}</dd>
			<dt class="faint">managed</dt><dd>{app.status?.state_routes ?? 0} routes</dd>
			<dt class="faint">caddy</dt>
			<dd style="color:{app.status?.caddy_ok ? 'var(--ok)' : 'var(--danger)'}">
				{app.status?.caddy_ok ? 'connected' : 'unreachable'}
			</dd>
		</dl>
		{#if app.status?.caddy_error}
			<p class="mt-3 border border-[color-mix(in_srgb,var(--danger)_30%,var(--line))] px-2 py-1.5 text-[11.5px] text-[var(--danger)]">
				{app.status.caddy_error}
			</p>
		{/if}
	</section>

	<section class="panel p-4">
		<h2 class="text-[13.5px] font-semibold">Appearance</h2>
		<p class="muted mt-1 text-[12px]">Dense, dark-first developer UI. The theme is stored in this browser.</p>
		<div class="mt-3 flex gap-2">
			<button class="btn {app.theme === 'dark' ? 'btn-primary' : ''}" onclick={() => setTheme('dark')}>
				<Moon size={13} /> Dark
			</button>
			<button class="btn {app.theme === 'light' ? 'btn-primary' : ''}" onclick={() => setTheme('light')}>
				<Sun size={13} /> Light
			</button>
		</div>
	</section>

	<section class="panel p-4 md:col-span-2">
		<h2 class="text-[13.5px] font-semibold">Start password</h2>
		<p class="muted mt-1 text-[12px]">
			Rotating prints the new password once on the server console and invalidates every other session. It is never
			written to a log file.
		</p>

		{#if newPassword}
			<div class="mt-3 border border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] bg-[color-mix(in_srgb,var(--warn)_6%,var(--panel))] p-3">
				<p class="mb-1 flex items-center gap-1.5 text-[11px] font-semibold text-[var(--warn)]">
					<ShieldAlert size={13} /> Shown once — store it now.
				</p>
				<div class="flex items-center gap-2">
					<code class="mono flex-1 select-all text-[13px]">{newPassword}</code>
					<button class="btn btn-sm" onclick={copy}>
						{#if copied}<Check size={12} />{:else}<Copy size={12} />{/if}
						{copied ? 'Copied' : 'Copy'}
					</button>
				</div>
			</div>
		{/if}

		<button class="btn mt-3" onclick={rotate} disabled={rotating}>
			{#if rotating}<span class="spinner"></span>{:else}<KeyRound size={13} />{/if}
			Rotate password
		</button>
	</section>
</div>
