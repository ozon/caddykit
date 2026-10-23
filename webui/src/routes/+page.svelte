<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, applyPreview, formatTime, preview, routeHref } from '$lib/api';
	import type { Preview, RouteView } from '$lib/types';
	import { app, toast } from '$lib/store.svelte';
	import Badge from '$lib/components/Badge.svelte';
	import DomainLink from '$lib/components/DomainLink.svelte';
	import PreviewDialog from '$lib/components/PreviewDialog.svelte';
	import {
		ArrowUpRight,
		FileWarning,
		Plus,
		RefreshCw,
		Search,
		Trash2,
		Unplug
	} from '@lucide/svelte';

	let routes = $state<RouteView[]>([]);
	let loading = $state(true);
	let error = $state('');
	let filter = $state('');
	let group = $state<'all' | 'managed' | 'conflicts' | 'foreign' | 'system'>('all');
	let searchEl = $state<HTMLInputElement>();
	let previewData = $state<Preview | null>(null);
	let previewOpen = $state(false);
	let busy = $state(false);
	let lastCmd = $state<{ kind: string; payload: Record<string, unknown> } | null>(null);

	const conflictStatus = new Set([
		'externally_modified',
		'missing_restoreable',
		'dup_id',
		'op_unclear',
		'foreign_collision',
		'missing_context'
	]);

	const counts = $derived.by(() => ({
		managed: routes.filter((r) => r.managed && !r.self).length,
		foreign: routes.filter((r) => !r.managed && !r.self).length,
		conflicts: routes.filter((r) => conflictStatus.has(r.status)).length,
		self: routes.filter((r) => r.self).length
	}));

	function rank(r: RouteView) {
		if (conflictStatus.has(r.status)) return 0;
		if (r.status === 'missing_restoreable') return 1;
		if (r.managed && !r.self) return 2;
		if (r.self) return 3;
		return 4;
	}

	const filtered = $derived.by(() => {
		const q = filter.trim().toLowerCase();
		return routes
			.filter((r) => {
				if (group === 'managed' && !(r.managed && !r.self)) return false;
				if (group === 'foreign' && (r.managed || r.self)) return false;
				if (group === 'system' && !r.self) return false;
				if (group === 'conflicts' && !conflictStatus.has(r.status)) return false;
				if (!q) return true;
				return (
					r.host.toLowerCase().includes(q) ||
					r.id.toLowerCase().includes(q) ||
					r.upstream.toLowerCase().includes(q) ||
					(r.paths ?? []).join(' ').toLowerCase().includes(q) ||
					r.status.toLowerCase().includes(q)
				);
			})
			.sort((a, b) => rank(a) - rank(b) || (a.host || '').localeCompare(b.host || ''));
	});

	async function load(silent = false) {
		if (!silent) loading = true;
		try {
			const res = await api<{ routes: RouteView[] }>('/api/v1/routes?all=1');
			routes = res.routes ?? [];
			app.routes = routes;
			error = '';
		} catch (err) {
			if (!silent) error = err instanceof Error ? err.message : 'Could not load routes';
		} finally {
			if (!silent) loading = false;
		}
	}

	async function openPreview(kind: string, payload: Record<string, unknown>) {
		lastCmd = { kind, payload };
		try {
			previewData = await preview(kind, payload);
			previewOpen = true;
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Preview failed');
		}
	}

	async function apply() {
		if (!previewData) return;
		busy = true;
		try {
			const res = await applyPreview(previewData);
			toast(res.message || 'Applied');
			previewOpen = false;
			previewData = null;
			await load(true);
		} catch (err: any) {
			if (err?.code === 'PREVIEW_STALE' && lastCmd) {
				toast('Caddy changed in the meantime — preview refreshed');
				await openPreview(lastCmd.kind, lastCmd.payload);
			} else {
				toast(err instanceof Error ? err.message : 'Apply failed');
			}
		} finally {
			busy = false;
		}
	}

	function openRoute(r: RouteView) {
		goto(routeHref(r.id || r.host));
	}

	onMount(() => {
		load();
		const timer = window.setInterval(() => load(true), 5000);
		const onVisibility = () => {
			if (!document.hidden) load(true);
		};
		document.addEventListener('visibilitychange', onVisibility);
		return () => {
			window.clearInterval(timer);
			document.removeEventListener('visibilitychange', onVisibility);
		};
	});

	$effect(() => {
		if (app.searchFocus > 0) searchEl?.focus();
	});
</script>

<svelte:head><title>Routes · caddykit</title></svelte:head>

<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
	<div>
		<div class="flex items-center gap-2">
			<h1 class="text-[18px] font-semibold tracking-tight">Routes</h1>
			<span class="badge mono">{app.status?.domain ?? '—'}</span>
		</div>
		<p class="muted mt-0.5 text-[12px]">
			Managed routes are restored automatically. Foreign routes are visible but untouched.
		</p>
	</div>
	<div class="flex items-center gap-2">
		<button class="btn" onclick={() => load()} title="Refresh">
			<RefreshCw size={13} /> Refresh
		</button>
		<button class="btn btn-primary" onclick={() => goto('/routes/new')}>
			<Plus size={14} /> New route
		</button>
	</div>
</div>

{#if counts.conflicts > 0}
	<a
		href="/conflicts"
		class="mb-4 flex items-center gap-2 border border-[color-mix(in_srgb,var(--warn)_38%,var(--line))] bg-[color-mix(in_srgb,var(--warn)_7%,var(--panel))] px-3 py-2 text-[12px]"
	>
		<FileWarning size={14} class="text-[var(--warn)]" />
		<span class="font-medium text-[var(--warn)]">{counts.conflicts} route{counts.conflicts === 1 ? '' : 's'} need attention</span>
		<span class="muted">— external changes, collisions or missing routes. Open the conflict center.</span>
		<span class="mono ml-auto text-[11px] text-[var(--warn)]">open →</span>
	</a>
{/if}

<div class="mb-3 flex flex-wrap items-center gap-2">
	<div class="relative min-w-[240px] flex-1 sm:max-w-[420px]">
		<Search size={13} class="pointer-events-none absolute top-1/2 left-2.5 -translate-y-1/2 text-[var(--faint)]" />
		<input
			bind:this={searchEl}
			bind:value={filter}
			class="input pl-7"
			placeholder="Filter host, id, upstream, status…"
			spellcheck="false"
		/>
		<span class="kbd absolute top-1/2 right-2 -translate-y-1/2">/</span>
	</div>
	<div class="flex items-center gap-1 text-[11px]">
		{#each [{ id: 'all', label: 'All', n: routes.length }, { id: 'managed', label: 'Managed', n: counts.managed }, { id: 'conflicts', label: 'Conflicts', n: counts.conflicts }, { id: 'foreign', label: 'Foreign', n: counts.foreign }, { id: 'system', label: 'System', n: counts.self }] as g}
			<button
				class="h-[26px] rounded-[3px] border px-2 font-medium {group === g.id
					? 'border-[var(--line-strong)] bg-[var(--panel-3)] text-[var(--fg)]'
					: 'border-transparent text-[var(--muted)] hover:bg-[var(--panel-2)]'}"
				onclick={() => (group = g.id as typeof group)}
			>
				{g.label} <span class="faint mono">{g.n}</span>
			</button>
		{/each}
	</div>
</div>

{#if error}
	<div class="panel border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] px-4 py-3 text-[12px] text-[var(--danger)]">
		{error}
	</div>
{:else if loading && routes.length === 0}
	<div class="panel flex items-center gap-2 px-4 py-8 text-[12px] text-[var(--muted)]">
		<span class="spinner"></span> Loading routes…
	</div>
{:else}
	<div class="panel overflow-x-auto">
		<table class="table">
			<thead>
				<tr>
					<th>Status</th>
					<th>Host</th>
					<th>Path</th>
					<th>Upstream</th>
					<th>Source</th>
					<th class="text-right">Actions</th>
				</tr>
			</thead>
			<tbody>
				{#each filtered as r (r.id || r.host)}
					<tr class="cursor-pointer" onclick={() => openRoute(r)}>
						<td class="whitespace-nowrap">
							<Badge status={r.status} />
						</td>
						<td>
							<DomainLink host={r.host} label={r.host || '—'} className="mono text-[12px]" />
							{#if r.id}
								<span class="faint mono ml-2 hidden text-[10px] xl:inline">{r.id.replace('caddykit/r/', '')}</span>
							{/if}
						</td>
						<td class="mono text-[11.5px] text-[var(--muted)]">
							{(r.paths ?? []).length ? (r.paths ?? []).join(', ') : '*'}
						</td>
						<td class="mono text-[11.5px]">
							{r.upstream || '—'}
							{#if r.route && (r.route.handle as any[] | undefined)?.some((h: any) => h?.transport?.tls)}
								<span class="badge badge-accent ml-1.5">tls</span>
							{/if}
						</td>
						<td class="text-[11px] text-[var(--muted)]">{r.source || (r.self ? 'lifecycle' : '—')}</td>
						<td>
							<div class="flex items-center justify-end gap-1">
								<button class="btn btn-sm" onclick={(e) => { e.stopPropagation(); openRoute(r); }} title="Open">
									<ArrowUpRight size={12} />
								</button>
								{#if !r.managed && !r.self}
									<button
										class="btn btn-sm"
										onclick={(e) => { e.stopPropagation(); openPreview('adopt', { id: r.id || r.host }); }}
										title="Adopt this route"
									>
										adopt
									</button>
								{/if}
								{#if r.managed && !r.self}
									<button
										class="btn btn-sm"
										onclick={(e) => { e.stopPropagation(); openPreview('unmanage', { id: r.id }); }}
										title="Unmanage (keep Caddy route)"
									>
										<Unplug size={12} />
									</button>
									<button
										class="btn btn-sm btn-danger"
										onclick={(e) => { e.stopPropagation(); openPreview('delete', { id: r.id }); }}
										title="Delete route"
									>
										<Trash2 size={12} />
									</button>
								{/if}
							</div>
						</td>
					</tr>
				{:else}
					<tr>
						<td colspan="6" class="py-10 text-center text-[12.5px] text-[var(--muted)]">
							{#if filter || group !== 'all'}
								No route matches this filter.
							{:else}
								No routes yet. <a class="link" href="/routes/new">Create the first one.</a>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>

	<div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[10.5px] text-[var(--faint)]">
		<span><span class="kbd">⌘</span> <span class="kbd">K</span> palette</span>
		<span><span class="kbd">g</span> <span class="kbd">d</span> dashboard</span>
		<span><span class="kbd">g</span> <span class="kbd">c</span> conflicts</span>
		<span><span class="kbd">/</span> filter</span>
		<span class="ml-auto mono">last sync {formatTime(app.status?.last_reconcile)}</span>
	</div>
{/if}

<PreviewDialog
	preview={previewData}
	open={previewOpen}
	{busy}
	onclose={() => (previewOpen = false)}
	onapply={apply}
/>
