<script lang="ts">
	import { onMount } from 'svelte';
	import { api } from '$lib/api';
	import type { Template } from '$lib/types';
	import { toast } from '$lib/store.svelte';
	import { ArrowUpRight, Sparkles } from '@lucide/svelte';

	let templates = $state<Template[]>([]);
	let loading = $state(true);

	onMount(async () => {
		try {
			const res = await api<{ templates: Template[] }>('/api/v1/templates');
			templates = res.templates ?? [];
		} catch (err) {
			toast(err instanceof Error ? err.message : 'Could not load templates');
		} finally {
			loading = false;
		}
	});
</script>

<svelte:head><title>Templates · devproxy</title></svelte:head>

<div class="mb-4 flex flex-wrap items-end justify-between gap-3">
	<div>
		<h1 class="text-[18px] font-semibold tracking-tight">Templates</h1>
		<p class="muted mt-0.5 text-[12px]">
			Declarative route recipes. No scripts. The generated route is managed independently afterwards.
		</p>
	</div>
	<a class="btn" href="/templates"><Sparkles size={13} /> Local directory overrides embedded defaults</a>
</div>

{#if loading}
	<div class="panel flex items-center gap-2 px-4 py-8 text-[12px] text-[var(--muted)]"><span class="spinner"></span> Loading templates…</div>
{:else if templates.length === 0}
	<div class="panel px-4 py-10 text-center text-[12.5px] text-[var(--muted)]">No templates found.</div>
{:else}
	<div class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
		{#each templates as t}
			<article class="panel flex flex-col p-4">
				<div class="flex items-start justify-between gap-3">
					<div>
						<h2 class="text-[14px] font-semibold">{t.name}</h2>
						<p class="mono faint mt-0.5 text-[10.5px]">{t.id} · schema v{t.schema_version}</p>
					</div>
					<a class="btn btn-sm" href="/routes/new/template?name={encodeURIComponent(t.id)}">
						Use <ArrowUpRight size={11} />
					</a>
				</div>
				<p class="muted mt-2 text-[12px]">{t.description}</p>

				{#if t.parameters?.length}
					<div class="mt-3">
						<p class="label mb-1">Parameters</p>
						<div class="flex flex-col gap-1">
							{#each t.parameters as p}
								<div class="flex items-baseline justify-between gap-2 text-[11.5px]">
									<span class="mono">{p.name}</span>
									<span class="faint mono text-[10px]">
										{p.type}{p.required ? ' · required' : ''}{p.default !== undefined ? ` · ${p.default}` : ''}
									</span>
								</div>
							{/each}
						</div>
					</div>
				{/if}

				{#if t.requirements?.length}
					<div class="mt-3 border-t border-[var(--line)] pt-2">
						<p class="label mb-1">Requirements</p>
						<ul class="flex flex-col gap-1 text-[11.5px] text-[var(--muted)]">
							{#each t.requirements as r}
								<li>— {r}</li>
							{/each}
						</ul>
					</div>
				{/if}
			</article>
		{/each}
	</div>
{/if}
