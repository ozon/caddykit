<script lang="ts">
	import { goto } from '$app/navigation';
	import { applyPreview, preview, routeHref } from '$lib/api';
	import type { Preview } from '$lib/types';
	import { toast } from '$lib/store.svelte';
	import { defaultRoute, validateRoute } from '$lib/route-edit';
	import RouteEditor from '$lib/components/RouteEditor.svelte';
	import CaddyfilePreview from '$lib/components/CaddyfilePreview.svelte';
	import PreviewDialog from '$lib/components/PreviewDialog.svelte';
	import { ArrowLeft, Save } from '@lucide/svelte';

	let draft = $state(defaultRoute());
	let previewData = $state<Preview | null>(null);
	let previewOpen = $state(false);
	let busy = $state(false);
	const errors = $derived(validateRoute(draft));

	async function openPreview() {
		if (errors.length) return;
		try {
			previewData = await preview('create', { route: draft });
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
			toast(res.message || 'Created');
			await goto(routeHref(res.route_id ?? ''));
		} catch (err: any) {
			if (err?.code === 'PREVIEW_STALE') {
				toast('Caddy changed in the meantime — preview refreshed');
				previewData = await preview('create', { route: draft });
				previewOpen = true;
			} else {
				toast(err instanceof Error ? err.message : 'Apply failed');
			}
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>New advanced route · caddykit</title></svelte:head>

<div class="mb-4">
	<a href="/routes/new" class="muted flex items-center gap-1 text-[11.5px] hover:text-[var(--fg)]"
		><ArrowLeft size={12} /> Simple create</a
	>
	<h1 class="mt-1 text-[18px] font-semibold tracking-tight">New route — advanced</h1>
	<p class="muted mt-0.5 text-[12px]">
		Edit the full handler tree. Additional fields and unknown handlers are preserved; the JSON tab on the route page
		remains the final fallback.
	</p>
</div>

<div class="grid items-start gap-4 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)]">
	<form
		class="panel p-4"
		onsubmit={(e) => {
			e.preventDefault();
			openPreview();
		}}
	>
		<RouteEditor bind:route={draft} />

		{#if errors.length}
			<div
				class="mt-3 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_6%,var(--panel))] px-3 py-2 text-[12px] text-[var(--danger)]"
			>
				<p class="label text-[var(--danger)]">Validation</p>
				<ul class="flex flex-col gap-1">
					{#each errors.slice(0, 10) as err}
						<li>— {err}</li>
					{/each}
					{#if errors.length > 10}
						<li>— … {errors.length - 10} more</li>
					{/if}
				</ul>
			</div>
		{/if}

		<div class="mt-3 flex flex-wrap items-center justify-between gap-2">
			<p class="hint">The server validates the route and requires a fresh preview before apply.</p>
			<button class="btn btn-primary" type="submit" disabled={errors.length > 0}>
				<Save size={13} /> Preview create
			</button>
		</div>
	</form>

	<div class="flex flex-col gap-3 lg:sticky lg:top-16">
		<CaddyfilePreview route={draft} />
		<div class="panel px-3 py-2 text-[11px] text-[var(--muted)]">
			The preview is read-only. Caddy accepts only JSON from caddykit; the Caddyfile view is never applied.
		</div>
	</div>
</div>

<PreviewDialog
	preview={previewData}
	open={previewOpen}
	{busy}
	onclose={() => (previewOpen = false)}
	onapply={apply}
/>
