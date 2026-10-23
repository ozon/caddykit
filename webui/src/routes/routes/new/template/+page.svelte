<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, applyPreview, preview, routeHref } from '$lib/api';
	import type { Preview, Template, TemplateParameter } from '$lib/types';
	import { toast } from '$lib/store.svelte';
	import Diff from '$lib/components/Diff.svelte';
	import { ArrowLeft, Sparkles } from '@lucide/svelte';

	let templates = $state<Template[]>([]);
	let selected = $state<Template | null>(null);
	let params = $state<Record<string, string>>({});
	let previewData = $state<Preview | null>(null);
	let error = $state('');
	let previewing = $state(false);
	let applying = $state(false);
	let loading = $state(true);

	function select(t: Template) {
		selected = t;
		const next: Record<string, string> = {};
		for (const p of t.parameters ?? []) {
			next[p.name] = p.default !== undefined && p.default !== null ? String(p.default) : '';
		}
		params = next;
		previewData = null;
		error = '';
	}

	async function loadPreview() {
		if (!selected) return;
		previewing = true;
		error = '';
		try {
			previewData = await preview('create', { template: selected.id, params });
		} catch (err) {
			previewData = null;
			error = err instanceof Error ? err.message : 'Preview failed';
		} finally {
			previewing = false;
		}
	}

	async function apply() {
		if (!previewData || applying) return;
		applying = true;
		try {
			const res = await applyPreview(previewData);
			toast(`Created ${res.route_id ?? 'route'}`);
			await goto(routeHref(res.route_id ?? ''));
		} catch (err: any) {
			if (err?.code === 'PREVIEW_STALE') {
				await loadPreview();
				toast('Route list changed — preview refreshed');
			} else {
				toast(err instanceof Error ? err.message : 'Apply failed');
			}
		} finally {
			applying = false;
		}
	}

	function inputType(p: TemplateParameter) {
		return p.sensitive ? 'password' : 'text';
	}

	onMount(async () => {
		try {
			const res = await api<{ templates: Template[] }>('/api/v1/templates');
			templates = res.templates ?? [];
			const name = page.url.searchParams.get('name');
			const found = templates.find((t) => t.id === name);
			if (found) select(found);
		} catch (err) {
			error = err instanceof Error ? err.message : 'Could not load templates';
		} finally {
			loading = false;
		}
	});
</script>

<svelte:head><title>New from template · caddykit</title></svelte:head>

<div class="mb-4">
	<a href="/routes/new" class="muted flex items-center gap-1 text-[11.5px] hover:text-[var(--fg)]"
		><ArrowLeft size={12} /> Manual route</a
	>
	<h1 class="mt-1 text-[18px] font-semibold tracking-tight">New route from template</h1>
</div>

{#if loading}
	<div class="panel flex items-center gap-2 px-4 py-8 text-[12px] text-[var(--muted)]"><span class="spinner"></span> Loading…</div>
{:else if !selected}
	<div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
		{#each templates as t}
			<button class="panel p-4 text-left hover:border-[var(--line-strong)]" onclick={() => select(t)}>
				<div class="flex items-center gap-2">
					<Sparkles size={14} class="text-[var(--accent)]" />
					<span class="text-[14px] font-semibold">{t.name}</span>
				</div>
				<p class="mono faint mt-0.5 text-[10.5px]">{t.id}</p>
				<p class="muted mt-2 text-[12px]">{t.description}</p>
				<p class="mono mt-3 text-[10.5px] text-[var(--accent)]">select →</p>
			</button>
		{/each}
	</div>
{:else}
	<div class="grid items-start gap-4 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
		<form
			class="panel p-4"
			onsubmit={(e) => {
				e.preventDefault();
				loadPreview();
			}}
		>
			<div class="flex items-center justify-between gap-2">
				<div>
					<h2 class="text-[14px] font-semibold">{selected.name}</h2>
					<p class="muted text-[11.5px]">{selected.description}</p>
				</div>
				<button type="button" class="btn btn-sm" onclick={() => (selected = null)}>Change</button>
			</div>

			<div class="mt-4 flex flex-col gap-3">
				{#each selected.parameters ?? [] as p}
					<div>
						<label class="label" for={`p-${p.name}`}>
							{p.name}
							{#if p.required}<span class="text-[var(--danger)]">*</span>{/if}
							{#if p.sensitive}<span class="badge badge-warn ml-1">sensitive</span>{/if}
						</label>
						<input
							id={`p-${p.name}`}
							bind:value={params[p.name]}
							class="input mono"
							type={inputType(p)}
							placeholder={p.type}
							spellcheck="false"
						/>
						{#if p.description}<p class="hint mt-1">{p.description}</p>{/if}
					</div>
				{/each}
			</div>

			<button class="btn btn-primary mt-4 w-full" type="submit" disabled={previewing}>
				{#if previewing}<span class="spinner"></span>{/if}
				Preview route
			</button>
			<p class="hint mt-2">
				Templates are rendered by plain <span class="mono">{'{{param}}'}</span> substitution after type validation. No
				scripts, no exec.
			</p>
		</form>

		<div class="panel flex min-h-[300px] flex-col lg:sticky lg:top-16">
			<div class="flex items-center justify-between border-b border-[var(--line)] px-4 py-2.5">
				<p class="label mb-0">Preview</p>
				{#if previewing}<span class="spinner"></span>{/if}
			</div>
			<div class="flex-1 p-3">
				{#if error}
					<p class="border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] px-2 py-1.5 text-[12px] text-[var(--danger)]">{error}</p>
				{:else if !previewData}
					<p class="muted px-1 py-8 text-center text-[12px]">Fill the parameters and preview the generated route.</p>
				{:else}
					{#if previewData.requirements?.length}
						<div class="panel-2 mb-3 p-2.5">
							<p class="label">Requirements</p>
							<ul class="flex flex-col gap-1 text-[11.5px] text-[var(--muted)]">
								{#each previewData.requirements as r}<li>— {r}</li>{/each}
							</ul>
						</div>
					{/if}
					{#if previewData.blocked}
						<p class="mb-2 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] px-2 py-1.5 text-[12px]">
							<span class="mono text-[var(--danger)]">{previewData.blocked.code}</span> — {previewData.blocked.message}
						</p>
					{/if}
					<Diff changes={previewData.diff} />
				{/if}
			</div>
			<div class="flex items-center justify-between gap-2 border-t border-[var(--line)] px-4 py-3">
				<span class="faint text-[10.5px]">{selected.id}</span>
				<button class="btn btn-primary" onclick={apply} disabled={!previewData || !!previewData.blocked || applying}>
					{#if applying}<span class="spinner"></span>{/if}
					Create route
				</button>
			</div>
		</div>
	</div>
{/if}
