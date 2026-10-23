<script lang="ts">
	import { goto } from '$app/navigation';
	import { app } from '$lib/store.svelte';
	import { api, routeHref } from '$lib/api';
	import type { RouteView } from '$lib/types';
	import {
		ArrowRight,
		FileWarning,
		LayoutDashboard,
		Plus,
		RotateCcw,
		Search,
		Settings,
		Sparkles
	} from '@lucide/svelte';

	type Item = { label: string; hint: string; run: () => void; icon: any };

	let dialog = $state<HTMLDialogElement>();
	let input = $state<HTMLInputElement>();
	let query = $state('');
	let active = $state(0);
	let routes = $state<RouteView[]>([]);

	const actions: Item[] = [
		{ label: 'Dashboard', hint: 'g d', run: () => goto('/'), icon: LayoutDashboard },
		{ label: 'New route', hint: 'n', run: () => goto('/routes/new'), icon: Plus },
		{ label: 'New advanced route', hint: '', run: () => goto('/routes/new/advanced'), icon: Plus },
		{ label: 'Conflicts', hint: 'g c', run: () => goto('/conflicts'), icon: FileWarning },
		{ label: 'Undo', hint: 'g u', run: () => goto('/undo'), icon: RotateCcw },
		{ label: 'Templates', hint: '', run: () => goto('/templates'), icon: Sparkles },
		{ label: 'Settings', hint: '', run: () => goto('/settings'), icon: Settings }
	];

	const matches = $derived.by(() => {
		const q = query.trim().toLowerCase();
		if (!q) return { actions, routes: routes.slice(0, 8) };
		return {
			actions: actions.filter((a) => a.label.toLowerCase().includes(q)),
			routes: routes
				.filter(
					(r) =>
						r.host.toLowerCase().includes(q) ||
						r.id.toLowerCase().includes(q) ||
						r.upstream.toLowerCase().includes(q) ||
						(r.paths ?? []).join(' ').toLowerCase().includes(q)
				)
				.slice(0, 10)
		};
	});

	const flat = $derived<Item[]>([
		...matches.actions,
		...matches.routes.map((r) => ({
			label: r.host || r.id,
			hint: r.upstream || r.status,
			icon: ArrowRight,
			run: () => goto(routeHref(r.id))
		}))
	]);

	$effect(() => {
		if (!dialog) return;
		if (app.paletteOpen && !dialog.open) {
			dialog.showModal();
			query = '';
			active = 0;
			load();
			queueMicrotask(() => input?.focus());
		}
		if (!app.paletteOpen && dialog.open) dialog.close();
	});

	$effect(() => {
		void query;
		active = 0;
	});

	async function load() {
		try {
			const res = await api<{ routes: RouteView[] }>('/api/v1/routes?all=1');
			routes = res.routes ?? [];
		} catch {
			/* the palette still offers navigation */
		}
	}

	function close() {
		app.paletteOpen = false;
	}

	function run(item: Item | undefined) {
		if (!item) return;
		close();
		item.run();
	}

	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'ArrowDown') {
			e.preventDefault();
			active = Math.min(active + 1, flat.length - 1);
		} else if (e.key === 'ArrowUp') {
			e.preventDefault();
			active = Math.max(active - 1, 0);
		} else if (e.key === 'Enter') {
			e.preventDefault();
			run(flat[active]);
		} else if (e.key === 'Escape') {
			e.preventDefault();
			close();
		}
	}
</script>

<dialog
	bind:this={dialog}
	onclose={close}
	onclick={(e) => {
		if (e.target === dialog) close();
	}}
>
	<div class="flex flex-col">
		<div class="flex items-center gap-2 border-b border-[var(--line)] px-3 py-2.5">
			<Search size={14} class="text-[var(--muted)]" />
			<input
				bind:this={input}
				bind:value={query}
				onkeydown={onkeydown}
				class="h-7 flex-1 border-0 bg-transparent text-[13.5px] outline-none placeholder:text-[var(--faint)]"
				placeholder="Jump to a route or run an action…"
				autocomplete="off"
				spellcheck="false"
			/>
			<span class="kbd">Esc</span>
		</div>
		<div class="max-h-[58vh] overflow-auto p-1.5">
			{#if flat.length === 0}
				<p class="muted px-3 py-6 text-center text-[12.5px]">Nothing matches.</p>
			{/if}
			{#each flat as item, i}
				<button
					class="flex w-full items-center gap-2.5 rounded-[3px] px-2.5 py-2 text-left text-[12.5px] {i === active
						? 'bg-[var(--panel-3)] text-[var(--fg)]'
						: 'text-[var(--muted)] hover:bg-[var(--panel-2)]'}"
					onclick={() => run(item)}
					onmouseenter={() => (active = i)}
				>
					<item.icon size={14} class="shrink-0 opacity-80" />
					<span class="mono min-w-0 flex-1 truncate">{item.label}</span>
					<span class="faint mono text-[10.5px]">{item.hint}</span>
				</button>
			{/each}
		</div>
	</div>
</dialog>
