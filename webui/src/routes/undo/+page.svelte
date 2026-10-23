<script lang="ts">
	import { onMount } from 'svelte';
	import { applyUndo, undoInfo } from '$lib/api';
	import type { UndoInfo } from '$lib/types';
	import { toast } from '$lib/store.svelte';
	import Badge from '$lib/components/Badge.svelte';
	import { RotateCcw, Undo2 } from '@lucide/svelte';

	let info = $state<UndoInfo | null>(null);
	let loading = $state(true);
	let busy = $state(false);
	let confirmOpen = $state(false);

	async function load() {
		loading = true;
		try {
			info = await undoInfo();
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not load undo');
		} finally {
			loading = false;
		}
	}

	async function run() {
		busy = true;
		try {
			const res = await applyUndo();
			toast(res.message || 'Undone');
			confirmOpen = false;
			await load();
		} catch (err: any) {
			confirmOpen = false;
			toast(err?.code === 'UNDO_BLOCKED' ? 'Undo blocked: the state changed since then' : err?.message ?? 'Undo failed');
			await load();
		} finally {
			busy = false;
		}
	}

	onMount(load);
</script>

<svelte:head><title>Undo · devproxy</title></svelte:head>

<div class="mb-4">
	<h1 class="text-[18px] font-semibold tracking-tight">Undo</h1>
	<p class="muted mt-0.5 text-[12px]">
		Exactly one user step can be reverted. System actions (restores, self-route lifecycle, password rotation) never
		overwrite it.
	</p>
</div>

{#if loading}
	<div class="panel flex items-center gap-2 px-4 py-8 text-[12px] text-[var(--muted)]"><span class="spinner"></span> Loading…</div>
{:else if !info?.available}
	<div class="panel flex flex-col items-center gap-2 px-4 py-12 text-center">
		<Undo2 size={22} class="text-[var(--faint)]" />
		<p class="text-[13px] font-medium">Nothing to undo</p>
		<p class="muted text-[12px]">Create, edit, delete, adopt or resolve a conflict first.</p>
		<a class="link mt-1 text-[12px]" href="/">Back to routes</a>
	</div>
{:else}
	<div class="panel max-w-[680px] p-5">
		<div class="flex items-start gap-3">
			<div class="mt-0.5 border border-[var(--line)] bg-[var(--panel-2)] p-2">
				<RotateCcw size={16} class="text-[var(--accent)]" />
			</div>
			<div class="min-w-0 flex-1">
				<div class="flex items-center gap-2">
					<Badge status="managed_sync" label={info.kind ?? 'step'} />
					<span class="faint mono text-[10.5px]">{info.created_at ? new Date(info.created_at).toLocaleString() : ''}</span>
				</div>
				<p class="mt-2 text-[13px]">{info.description || 'Last user operation'}</p>
				<p class="muted mt-1 text-[12px]">
					Undo re-reads Caddy first. If the route no longer matches the recorded after-state, the undo is refused
					instead of overwriting external changes.
				</p>
			</div>
		</div>

		<div class="mt-5 flex items-center justify-end gap-2 border-t border-[var(--line)] pt-3">
			{#if !confirmOpen}
				<button class="btn btn-primary" onclick={() => (confirmOpen = true)}>
					<RotateCcw size={13} /> Undo this step
				</button>
			{:else}
				<span class="muted mr-auto text-[11.5px]">This cannot be redone.</span>
				<button class="btn" onclick={() => (confirmOpen = false)} disabled={busy}>Cancel</button>
				<button class="btn btn-danger" onclick={run} disabled={busy}>
					{#if busy}<span class="spinner"></span>{/if} Confirm undo
				</button>
			{/if}
		</div>
	</div>
{/if}
