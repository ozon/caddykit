<script lang="ts">
	import { renderCaddyfile } from '$lib/api';
	import { toast } from '$lib/store.svelte';

	let { route }: { route: any } = $props();

	const snapshot = $derived(JSON.stringify(route ?? null));
	let text = $state('');
	let warnings = $state<string[]>([]);
	let error = $state('');
	let loading = $state(false);
	let seq = 0;

	$effect(() => {
		const my = ++seq;
		const payload = snapshot;
		if (!route) {
			text = '';
			warnings = [];
			error = '';
			loading = false;
			return;
		}
		const timer = window.setTimeout(async () => {
			loading = true;
			try {
				const parsed = JSON.parse(payload);
				const res = await renderCaddyfile(parsed);
				if (my === seq) {
					text = res.caddyfile ?? '';
					warnings = res.warnings ?? [];
					error = '';
				}
			} catch (err) {
				if (my === seq) {
					text = '';
					warnings = [];
					error = err instanceof Error ? err.message : 'Could not render the Caddyfile preview';
				}
			} finally {
				if (my === seq) loading = false;
			}
		}, 250);
		return () => window.clearTimeout(timer);
	});

	async function copy() {
		if (!text) return;
		try {
			await navigator.clipboard.writeText(text);
			toast('Caddyfile copied');
		} catch {
			toast('Could not copy to the clipboard');
		}
	}
</script>

<div class="panel p-3">
	<div class="flex items-center justify-between gap-2">
		<div class="flex items-center gap-2">
			<p class="label mb-0">Caddyfile preview</p>
			{#if loading}<span class="spinner"></span>{/if}
		</div>
		<button class="btn btn-sm" onclick={copy} disabled={!text}>Copy</button>
	</div>

	<div
		class="mt-2 border border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] bg-[color-mix(in_srgb,var(--warn)_6%,var(--panel))] px-2.5 py-2 text-[11.5px] text-[var(--warn)]"
	>
		Read-only approximation. Caddy cannot export JSON to a Caddyfile; unsupported fields appear as comments.
		Never apply this without validating it.
	</div>

	{#if error}
		<p
			class="mt-2 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_6%,var(--panel))] px-2.5 py-2 text-[11.5px] text-[var(--danger)]"
		>
			{error}
		</p>
	{/if}

	{#if warnings.length}
		<ul class="mt-2 flex flex-col gap-1 border border-[var(--line)] px-2.5 py-2 text-[11px] text-[var(--warn)]">
			{#each warnings as warning}
				<li>— {warning}</li>
			{/each}
		</ul>
	{/if}

	<pre
		class="panel-2 mono mt-2 max-h-[60vh] overflow-auto p-3 text-[11px] leading-relaxed whitespace-pre">{text ||
			(loading ? 'Rendering…' : 'No preview yet.')}</pre>
</div>
