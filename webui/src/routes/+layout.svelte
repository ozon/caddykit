<script lang="ts">
	import '../app.css';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, fetchStatus, formatTime, logout, readCsrfCookie, setCsrf } from '$lib/api';
	import { app, setTheme, toast } from '$lib/store.svelte';
	import CommandPalette from '$lib/components/CommandPalette.svelte';
	import {
		Command,
		FileWarning,
		LayoutDashboard,
		LogOut,
		Moon,
		RotateCcw,
		Settings,
		Sparkles,
		Sun
	} from '@lucide/svelte';

	let { children } = $props();
	const isLogin = $derived(page.url.pathname === '/login');
	let ready = $state(false);

	const nav = [
		{ href: '/', label: 'Dashboard', icon: LayoutDashboard },
		{ href: '/conflicts', label: 'Conflicts', icon: FileWarning },
		{ href: '/undo', label: 'Undo', icon: RotateCcw },
		{ href: '/templates', label: 'Templates', icon: Sparkles },
		{ href: '/settings', label: 'Settings', icon: Settings }
	];

	function active(href: string) {
		if (href === '/') return page.url.pathname === '/';
		return page.url.pathname === href || page.url.pathname.startsWith(href + '/');
	}

	async function poll() {
		if (isLogin) return;
		try {
			app.status = await fetchStatus();
		} catch {
			/* the banner shows the last known state */
		}
	}

	onMount(() => {
		const saved = localStorage.getItem('devproxy.theme');
		setTheme(saved === 'light' ? 'light' : 'dark');
		readCsrfCookie();

		(async () => {
			if (!isLogin) {
				try {
					const s = await api<{ csrf: string }>('/api/v1/session');
					setCsrf(s.csrf);
					await poll();
				} catch {
					/* api() redirects on 401 */
				}
			}
			ready = true;
		})();

		const timer = window.setInterval(poll, 5000);
		const onVisibility = () => {
			if (!document.hidden) poll();
		};
		document.addEventListener('visibilitychange', onVisibility);

		let lastG = 0;
		const onKey = (e: KeyboardEvent) => {
			const target = e.target as HTMLElement | null;
			const typing =
				!!target &&
				(target.tagName === 'INPUT' ||
					target.tagName === 'TEXTAREA' ||
					target.tagName === 'SELECT' ||
					target.isContentEditable);
			if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
				e.preventDefault();
				app.paletteOpen = !app.paletteOpen;
				return;
			}
			if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
			if (e.key === '/') {
				e.preventDefault();
				app.searchFocus++;
				return;
			}
			if (e.key === 'g') {
				lastG = Date.now();
				return;
			}
			if (Date.now() - lastG < 900) {
				if (e.key === 'd') goto('/');
				if (e.key === 'c') goto('/conflicts');
				if (e.key === 'u') goto('/undo');
				lastG = 0;
			}
		};
		window.addEventListener('keydown', onKey);

		return () => {
			window.clearInterval(timer);
			document.removeEventListener('visibilitychange', onVisibility);
			window.removeEventListener('keydown', onKey);
		};
	});
</script>

<svelte:head>
	<title>devproxy</title>
	<meta name="description" content="Manage Caddy proxy routes for local developer services." />
</svelte:head>

<div class="min-h-screen">
	<header
		class="sticky top-0 z-20 border-b border-[var(--line)] bg-[color-mix(in_srgb,var(--bg)_88%,transparent)] backdrop-blur-md"
	>
		<div class="mx-auto flex h-12 max-w-[1320px] items-center gap-5 px-4 sm:px-6">
			<a href="/" class="flex items-center gap-2" aria-label="devproxy home">
				<span
					class="mono flex h-6 w-6 items-center justify-center border border-[var(--accent)] text-[11px] font-bold text-[var(--accent)]"
					>dp</span
				>
				<span class="mono text-[13px] font-semibold tracking-tight">devproxy</span>
			</a>

			{#if !isLogin}
				<nav class="flex items-center gap-0.5" aria-label="Main">
					{#each nav as item}
						<a
							href={item.href}
							aria-label={item.label}
							aria-current={active(item.href) ? 'page' : undefined}
							class="flex h-7 items-center gap-1.5 rounded-[3px] px-2 text-[12px] {active(item.href)
								? 'bg-[var(--panel-3)] text-[var(--fg)]'
								: 'text-[var(--muted)] hover:bg-[var(--panel-2)] hover:text-[var(--fg)]'}"
						>
							<item.icon size={13} />
							<span class="hidden lg:inline">{item.label}</span>
							{#if item.href === '/conflicts' && app.status && app.status.conflicts > 0}
								<span class="badge badge-danger h-[17px] px-1.5 text-[9.5px]">{app.status.conflicts}</span>
							{/if}
						</a>
					{/each}
				</nav>
			{/if}

			<div class="flex-1"></div>

			{#if !isLogin && app.status}
				<div class="hidden items-center gap-3 text-[11px] md:flex">
					<span class="flex items-center gap-1.5" title={app.status.caddy_error ?? 'Caddy connection'}>
						<span
							class="h-1.5 w-1.5 rounded-full"
							style="background:{app.status.caddy_ok ? 'var(--ok)' : 'var(--danger)'}"
						></span>
						<span class="mono muted">{app.status.caddy_ok ? 'caddy ok' : 'caddy down'}</span>
					</span>
					<span class="faint mono hidden lg:inline">{app.status.server}</span>
					<span class="faint mono hidden xl:inline">sync {formatTime(app.status.last_reconcile)}</span>
					<a href="/conflicts" class="mono faint hover:text-[var(--fg)]">conflicts {app.status.conflicts}</a>
				</div>
			{/if}

			{#if !isLogin}
				<button
					class="btn btn-sm gap-1.5"
					onclick={() => (app.paletteOpen = true)}
					title="Command palette (⌘K)"
				>
					<Command size={12} />
					<span class="hidden sm:inline">K</span>
				</button>
				<button
					class="btn btn-sm px-2"
					onclick={() => setTheme(app.theme === 'dark' ? 'light' : 'dark')}
					title="Toggle theme"
					aria-label="Toggle theme"
				>
					{#if app.theme === 'dark'}<Sun size={13} />{:else}<Moon size={13} />{/if}
				</button>
				<button
					class="btn btn-sm px-2"
					title="Sign out"
					aria-label="Sign out"
					onclick={async () => {
						await logout();
						toast('Signed out');
					}}
				>
					<LogOut size={13} />
				</button>
			{/if}
		</div>
	</header>

	{#if !isLogin && app.status && !app.status.caddy_ok}
		<div
			class="border-b border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_8%,var(--bg))]"
		>
			<div class="mx-auto max-w-[1320px] px-4 py-1.5 text-[11.5px] sm:px-6">
				<span class="mono font-semibold text-[var(--danger)]">Caddy unreachable</span>
				<span class="muted"> — {app.status.caddy_error ?? 'the admin API is not responding'}</span>
			</div>
		</div>
	{/if}

	<main class="mx-auto max-w-[1320px] px-4 py-5 sm:px-6">
		{#if isLogin}
			{@render children()}
		{:else if ready}
			{@render children()}
		{:else}
			<div class="flex items-center gap-2 py-16 text-[var(--muted)]">
				<span class="spinner"></span> Loading devproxy…
			</div>
		{/if}
	</main>
</div>

<CommandPalette />

{#if app.toast}
	<div
		class="fixed right-4 bottom-4 z-50 max-w-md border border-[var(--line-strong)] bg-[var(--panel)] px-3 py-2 text-[12px] shadow-[var(--shadow)]"
		role="status"
	>
		{app.toast}
	</div>
{/if}
