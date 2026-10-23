<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, applyPreview, resolvePreview } from '$lib/api';
	import type { Conflict, Preview } from '$lib/types';
	import { app, toast } from '$lib/store.svelte';
	import Badge from '$lib/components/Badge.svelte';
	import PreviewDialog from '$lib/components/PreviewDialog.svelte';
	import { CheckCircle2, RefreshCw } from '@lucide/svelte';

	let conflicts = $state<Conflict[]>([]);
	let loading = $state(true);
	let error = $state('');
	let previewData = $state<Preview | null>(null);
	let previewOpen = $state(false);
	let busy = $state(false);
	let lastAction = $state<{ id: string; action: string } | null>(null);

	const highlight = $derived(page.url.searchParams.get('highlight') ?? '');

	const actions = [
		{
			id: 'take-current',
			label: 'Take current',
			desc: 'Store the Caddy state as the new desired state.'
		},
		{
			id: 'apply-desired',
			label: 'Apply desired',
			desc: 'Write the stored desired route back to Caddy.'
		},
		{
			id: 'unmanage',
			label: 'Unmanage',
			desc: 'Drop the binding. Caddy stays unchanged.'
		}
	];

	function reason(c: Conflict, action: string) {
		return c.blocked?.[action];
	}

	async function load() {
		loading = true;
		try {
			const res = await apiConflicts();
			conflicts = res;
			app.conflicts = res;
			error = '';
		} catch (err) {
			error = err instanceof Error ? err.message : 'Could not load conflicts';
		} finally {
			loading = false;
		}
	}

	async function apiConflicts(): Promise<Conflict[]> {
		const res = await api<{ conflicts: Conflict[] }>('/api/v1/conflicts');
		return res.conflicts ?? [];
	}

	async function openPreview(id: string, action: string) {
		lastAction = { id, action };
		try {
			previewData = await resolvePreview(id, action);
			previewOpen = true;
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Preview failed');
		}
	}

	async function apply() {
		if (!previewData || busy) return;
		busy = true;
		try {
			const res = await applyPreview(previewData);
			toast(res.message || 'Resolved');
			previewOpen = false;
			previewData = null;
			await load();
		} catch (err: any) {
			if (err?.code === 'PREVIEW_STALE' && lastAction) {
				toast('Caddy changed in the meantime — preview refreshed');
				await openPreview(lastAction.id, lastAction.action);
			} else {
				toast(err instanceof Error ? err.message : 'Resolve failed');
			}
		} finally {
			busy = false;
		}
	}

	function routeJson(c: Conflict, key: 'current' | 'desired'): string {
		const v = c[key];
		if (!v) return '—';
		const hosts = (v.match as any[])?.[0]?.host ?? [];
		const dial = ((v.handle as any[]) ?? []).find((h) => h?.handler === 'reverse_proxy')?.upstreams?.[0]?.dial;
		return `${(hosts as string[]).join(',') || '*'} → ${dial ?? '—'}`;
	}

	onMount(load);
</script>

<svelte:head><title>Conflicts · devproxy</title></svelte:head>

<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
	<div>
		<div class="flex items-center gap-2">
			<h1 class="text-[18px] font-semibold tracking-tight">Conflict center</h1>
			{#if conflicts.length}<span class="badge badge-warn">{conflicts.length}</span>{/if}
		</div>
		<p class="muted mt-0.5 text-[12px]">
			Nothing is overwritten automatically. Every action re-reads Caddy, shows a fresh diff and asks for confirmation.
		</p>
	</div>
	<button class="btn" onclick={() => load()}><RefreshCw size={13} /> Refresh</button>
</div>

{#if error}
	<div class="panel px-4 py-3 text-[12px] text-[var(--danger)]">{error}</div>
{:else if loading && conflicts.length === 0}
	<div class="panel flex items-center gap-2 px-4 py-8 text-[12px] text-[var(--muted)]"><span class="spinner"></span> Checking…</div>
{:else if conflicts.length === 0}
	<div class="panel flex flex-col items-center gap-2 px-4 py-12 text-center">
		<CheckCircle2 size={22} class="text-[var(--ok)]" />
		<p class="text-[13px] font-medium">No conflicts</p>
		<p class="muted text-[12px]">Managed routes match Caddy, or are safely restorable.</p>
	</div>
{:else}
	<div class="flex flex-col gap-3">
		{#each conflicts as c (c.route_id)}
			<article
				id={c.route_id}
				class="panel p-4 {highlight === c.route_id
					? 'border-[var(--accent)] shadow-[0_0_0_1px_var(--accent)]'
					: ''}"
			>
				<div class="flex flex-wrap items-start justify-between gap-4">
					<div class="min-w-0">
						<div class="flex flex-wrap items-center gap-2">
							<Badge status={c.kind.toLowerCase()} label={c.kind} />
							<h2 class="mono text-[13.5px] font-semibold">{c.host || c.route_id}</h2>
							<a href="/routes/{c.route_id.split('/').map(encodeURIComponent).join('/')}" class="faint mono text-[10.5px] hover:text-[var(--fg)]">
								{c.route_id}
							</a>
						</div>
						<p class="muted mt-1.5 text-[12px]">{c.reason}</p>
						<div class="mono mt-2 grid gap-1 text-[11px] sm:grid-cols-2">
							<div><span class="faint">caddy&nbsp;&nbsp;</span> {routeJson(c, 'current')}</div>
							<div><span class="faint">desired</span> {routeJson(c, 'desired')}</div>
						</div>
					</div>
					<div class="flex shrink-0 flex-wrap items-center gap-1.5">
						{#each actions as a}
							{@const blocked = reason(c, a.id)}
							<button
								class="btn btn-sm"
								class:btn-primary={a.id === 'take-current'}
								disabled={!!blocked}
								title={blocked ? `${blocked.code}: ${blocked.message}` : a.desc}
								onclick={() => openPreview(c.route_id, a.id)}
							>
								{a.label}
							</button>
						{/each}
					</div>
				</div>
				{#if Object.keys(c.blocked ?? {}).length}
					<div class="mt-3 flex flex-wrap gap-x-4 gap-y-1 border-t border-[var(--line)] pt-2 text-[10.5px]">
						{#each Object.entries(c.blocked ?? {}) as [action, blocked]}
							<span class="faint">
								<span class="mono">{action}</span> blocked:
								<span class="mono text-[var(--danger)]">{blocked.code}</span>
								{blocked.message}
							</span>
						{/each}
					</div>
				{/if}
			</article>
		{/each}
	</div>
{/if}

<PreviewDialog
	preview={previewData}
	open={previewOpen}
	{busy}
	onclose={() => (previewOpen = false)}
	onapply={apply}
/>
