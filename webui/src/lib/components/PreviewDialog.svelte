<script lang="ts">
	import type { Preview } from '$lib/types';
	import Diff from './Diff.svelte';
	import CaddyfilePreview from './CaddyfilePreview.svelte';

	let {
		preview,
		open = false,
		busy = false,
		onclose,
		onapply
	}: {
		preview: Preview | null;
		open?: boolean;
		busy?: boolean;
		onclose: () => void;
		onapply: () => void;
	} = $props();

	let dialog = $state<HTMLDialogElement>();
	let view = $state<'diff' | 'caddyfile'>('diff');

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) {
			view = 'diff';
			dialog.showModal();
		}
		if (!open && dialog.open) dialog.close();
	});
</script>

<dialog
	bind:this={dialog}
	onclose={() => onclose()}
	oncancel={(e) => {
		e.preventDefault();
		onclose();
	}}
	onclick={(e) => {
		if (e.target === dialog) onclose();
	}}
>
	{#if preview}
		<div class="flex max-h-[82vh] flex-col">
			<header class="flex items-start justify-between gap-6 border-b border-[var(--line)] px-5 py-4">
				<div>
					<p class="faint mono text-[10px] tracking-[0.14em] uppercase">Preview · {preview.kind}</p>
					<h2 class="mt-1 text-[15px] font-semibold">{preview.description}</h2>
				</div>
				<button class="btn btn-sm" onclick={onclose} aria-label="Close">Esc</button>
			</header>

			<div class="min-h-0 flex-1 overflow-auto px-5 py-4">
				{#if preview.blocked}
					<div class="mb-4 border border-[color-mix(in_srgb,var(--danger)_40%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_8%,var(--panel))] px-3 py-2 text-[12px]">
						<span class="mono text-[var(--danger)]">{preview.blocked.code}</span>
						<span class="muted"> — {preview.blocked.message}</span>
					</div>
				{/if}

				{#if preview.warnings?.length}
					<ul class="mb-4 flex flex-col gap-1 border border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] bg-[color-mix(in_srgb,var(--warn)_6%,var(--panel))] px-3 py-2 text-[12px]">
						{#each preview.warnings as warning}
							<li class="text-[var(--warn)]">{warning}</li>
						{/each}
					</ul>
				{/if}

				{#if preview.requirements?.length}
					<div class="panel-2 mb-4 px-3 py-2">
						<p class="label mb-1">Requirements</p>
						<ul class="flex flex-col gap-1 text-[12px]">
							{#each preview.requirements as requirement}
								<li class="muted">— {requirement}</li>
							{/each}
						</ul>
					</div>
				{/if}

				<div class="mb-3 flex items-center gap-1 border-b border-[var(--line)]">
					<button
						class="-mb-px border-b-2 px-3 py-2 text-[12px] {view === 'diff'
							? 'border-[var(--accent)] text-[var(--fg)]'
							: 'border-transparent text-[var(--muted)] hover:text-[var(--fg)]'}"
						onclick={() => (view = 'diff')}>Diff</button
					>
					<button
						class="-mb-px border-b-2 px-3 py-2 text-[12px] {view === 'caddyfile'
							? 'border-[var(--accent)] text-[var(--fg)]'
							: 'border-transparent text-[var(--muted)] hover:text-[var(--fg)]'}"
						onclick={() => (view = 'caddyfile')}>Caddyfile</button
					>
				</div>

				{#if view === 'diff'}
					<div class="panel-2 p-2">
						<Diff changes={preview.diff ?? []} />
					</div>
				{:else if preview.route}
					<CaddyfilePreview route={preview.route} />
				{:else}
					<div class="panel-2 p-3 text-[12px] text-[var(--muted)]">
						This operation removes the route; there is no route to render as a Caddyfile.
					</div>
				{/if}
			</div>

			<footer class="flex items-center justify-end gap-2 border-t border-[var(--line)] px-5 py-3">
				<button class="btn" onclick={onclose} disabled={busy}>Cancel</button>
				<button class="btn btn-primary" onclick={onapply} disabled={busy || !!preview.blocked}>
					{#if busy}<span class="spinner"></span>{/if}
					Apply
				</button>
			</footer>
		</div>
	{/if}
</dialog>
