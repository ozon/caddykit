<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, applyPreview, preview } from '$lib/api';
	import type { Preview, RouteView } from '$lib/types';
	import { toast } from '$lib/store.svelte';
	import { validateRoute } from '$lib/route-edit';
	import RouteEditor from '$lib/components/RouteEditor.svelte';
	import DomainLink from '$lib/components/DomainLink.svelte';
	import CaddyfilePreview from '$lib/components/CaddyfilePreview.svelte';
	import Badge from '$lib/components/Badge.svelte';
	import PreviewDialog from '$lib/components/PreviewDialog.svelte';
	import { ArrowLeft, FileWarning, Save, Trash2, Unplug } from '@lucide/svelte';

	const id = $derived(page.params.id ?? '');

	let view = $state<RouteView | null>(null);
	let draft = $state<any>(null);
	let loading = $state(true);
	let error = $state('');
	let tab = $state<'form' | 'json' | 'caddyfile'>('form');
	let jsonText = $state('');
	let jsonError = $state('');
	let parsed = $state<any>(null);
	let previewData = $state<Preview | null>(null);
	let previewOpen = $state(false);
	let busy = $state(false);
	let lastCmd = $state<{ kind: string; payload: Record<string, unknown> } | null>(null);

	const self = $derived(!!view?.self);
	const missing = $derived(!!view && !view.route);
	const editable = $derived(!!view && !self && !missing);
	const foreign = $derived(!!view && !view.managed && !view.self);
	const formErrors = $derived(draft ? validateRoute(draft) : []);

	async function load() {
		loading = true;
		try {
			const v = await api<RouteView>(`/api/v1/routes?id=${encodeURIComponent(id)}`);
			view = v;
			draft = v.route ? JSON.parse(JSON.stringify(v.route)) : null;
			jsonText = JSON.stringify(v.route ?? {}, null, 2);
			validateJSON(jsonText);
			error = '';
		} catch (err) {
			error = err instanceof Error ? err.message : 'Could not load route';
		} finally {
			loading = false;
		}
	}

	function validateJSON(text: string) {
		if (!text.trim()) {
			parsed = null;
			jsonError = 'Route JSON is empty';
			return;
		}
		try {
			parsed = JSON.parse(text);
			jsonError = '';
		} catch (err) {
			parsed = null;
			jsonError = err instanceof Error ? err.message : 'Invalid JSON';
		}
	}

	function syncJSONFromDraft() {
		jsonText = JSON.stringify(draft ?? {}, null, 2);
		validateJSON(jsonText);
	}

	function onJSONChange(value: string) {
		jsonText = value;
		validateJSON(value);
		if (parsed) draft = parsed;
	}

	function selectTab(next: 'form' | 'json' | 'caddyfile') {
		if (next === 'json') syncJSONFromDraft();
		tab = next;
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
		if (!previewData || busy) return;
		busy = true;
		try {
			const kind = previewData.kind;
			const res = await applyPreview(previewData);
			toast(res.message || 'Applied');
			previewOpen = false;
			previewData = null;
			if (kind === 'delete' || kind === 'unmanage') {
				await goto('/');
			} else {
				await load();
			}
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

	function submitForm(e: SubmitEvent) {
		e.preventDefault();
		if (!view || !editable || !draft || formErrors.length) return;
		openPreview('edit', { id: view.id || id, route: draft });
	}

	function submitJSON() {
		if (!view || !editable || !parsed) return;
		openPreview('edit', { id: view.id || id, route: parsed });
	}

	onMount(load);
</script>

<svelte:head><title>{view?.host || 'Route'} · caddykit</title></svelte:head>

{#if loading}
	<div class="panel flex items-center gap-2 px-4 py-8 text-[12px] text-[var(--muted)]">
		<span class="spinner"></span> Loading route…
	</div>
{:else if error}
	<div class="panel px-4 py-4 text-[12px] text-[var(--danger)]">{error}</div>
{:else if view}
	<div class="mb-4">
		<a href="/" class="muted flex items-center gap-1 text-[11.5px] hover:text-[var(--fg)]"
			><ArrowLeft size={12} /> All routes</a
		>
		<div class="mt-1 flex flex-wrap items-start justify-between gap-3">
			<div>
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="mono text-[18px] font-semibold tracking-tight">
						<DomainLink host={view.host} label={view.host || view.id} />
					</h1>
					<Badge status={view.status} />
					{#if self}<span class="badge badge-info">system route</span>{/if}
					{#if view.managed && !self}<span class="badge">managed</span>{/if}
					{#if foreign}<span class="badge">foreign</span>{/if}
				</div>
				<p class="mono faint mt-1 text-[11px]">{view.id || 'no @id'} · {view.upstream || 'nested handlers'}</p>
			</div>
			<div class="flex flex-wrap items-center gap-1.5">
				{#if !view.managed && !self && !missing}
					<button class="btn" onclick={() => openPreview('adopt', { id: view?.id || view?.host })}>
						Adopt route
					</button>
				{/if}
				{#if view.managed && !self}
					<button class="btn" onclick={() => openPreview('unmanage', { id: view?.id })}>
						<Unplug size={13} /> Unmanage
					</button>
					<button class="btn btn-danger" onclick={() => openPreview('delete', { id: view?.id })}>
						<Trash2 size={13} /> Delete
					</button>
				{/if}
			</div>
		</div>
	</div>

	{#if self}
		<div class="panel mb-4 border-[color-mix(in_srgb,var(--info)_30%,var(--line))] px-4 py-3 text-[12px]">
			<span class="text-[var(--info)]">This route is managed by the web lifecycle.</span>
			<span class="muted">
				On start it is pointed at the backend, on shutdown it becomes a static 503. Edits, unmanage and delete are
				blocked here and in the CLI.
			</span>
		</div>
	{:else if missing}
		<div class="panel mb-4 flex items-center gap-2 px-4 py-3 text-[12px]">
			<FileWarning size={14} class="text-[var(--danger)]" />
			<span>This managed route is missing in Caddy.</span>
			<a class="link" href="/conflicts?highlight={encodeURIComponent(view.id)}">Resolve it in the conflict center →</a>
		</div>
	{:else if view.status === 'externally_modified' || view.status === 'dup_id' || view.status === 'op_unclear'}
		<div
			class="panel mb-4 flex flex-wrap items-center gap-2 border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] px-4 py-3 text-[12px]"
		>
			<FileWarning size={14} class="text-[var(--warn)]" />
			<span>Caddy differs from the desired state.</span>
			<a class="link" href="/conflicts?highlight={encodeURIComponent(view.id)}">Open conflict center →</a>
			<span class="hint">Saving overwrites Caddy after a fresh preview; conflicts offer take-current.</span>
		</div>
	{/if}

	{#if !missing}
		<div class="mb-3 flex flex-wrap items-center gap-1 border-b border-[var(--line)]">
			<button
				class="-mb-px border-b-2 px-3 py-2 text-[12px] {tab === 'form'
					? 'border-[var(--accent)] text-[var(--fg)]'
					: 'border-transparent text-[var(--muted)] hover:text-[var(--fg)]'}"
				onclick={() => selectTab('form')}>Form</button
			>
			<button
				class="-mb-px border-b-2 px-3 py-2 text-[12px] {tab === 'json'
					? 'border-[var(--accent)] text-[var(--fg)]'
					: 'border-transparent text-[var(--muted)] hover:text-[var(--fg)]'}"
				onclick={() => selectTab('json')}>JSON</button
			>
			<button
				class="-mb-px border-b-2 px-3 py-2 text-[12px] {tab === 'caddyfile'
					? 'border-[var(--accent)] text-[var(--fg)]'
					: 'border-transparent text-[var(--muted)] hover:text-[var(--fg)]'}"
				onclick={() => selectTab('caddyfile')}>Caddyfile</button
			>
		</div>

		{#if tab === 'form'}
			{#if !draft}
				<div class="panel px-4 py-4 text-[12px] text-[var(--muted)]">No route object is available.</div>
			{:else}
				<form class="panel p-4" onsubmit={submitForm}>
					{#if foreign}
						<div
							class="mb-3 border border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] bg-[color-mix(in_srgb,var(--warn)_6%,var(--panel))] px-3 py-2 text-[12px] text-[var(--warn)]"
						>
							Saving this form adopts the foreign route. It will be restored automatically if it disappears
							externally.
						</div>
					{/if}
					<RouteEditor bind:route={draft} readonly={!editable} />
					{#if formErrors.length}
						<div
							class="mt-3 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_6%,var(--panel))] px-3 py-2 text-[12px] text-[var(--danger)]"
						>
							<p class="label text-[var(--danger)]">Validation</p>
							<ul class="flex flex-col gap-1">
								{#each formErrors.slice(0, 10) as err}
									<li>— {err}</li>
								{/each}
								{#if formErrors.length > 10}
									<li>— … {formErrors.length - 10} more</li>
								{/if}
							</ul>
						</div>
					{/if}
					{#if editable}
						<div class="mt-3 flex flex-wrap items-center justify-between gap-2">
							<p class="hint">Preview is mandatory; the server re-reads Caddy before apply.</p>
							<button class="btn btn-primary" type="submit" disabled={formErrors.length > 0}>
								<Save size={13} /> Preview changes
							</button>
						</div>
					{:else if self}
						<p class="hint mt-3">This system route is read-only.</p>
					{/if}
				</form>
			{/if}
		{:else if tab === 'json'}
			<div class="panel p-4">
				<div
					class="mb-3 border border-[color-mix(in_srgb,var(--warn)_35%,var(--line))] bg-[color-mix(in_srgb,var(--warn)_6%,var(--panel))] px-3 py-2 text-[12px]"
				>
					The JSON editor can show sensitive header values. It edits only this route object — not the global Caddy
					configuration.
				</div>
				{#if self}
					<pre class="panel-2 mono max-h-[60vh] overflow-auto p-3 text-[11px]">{jsonText}</pre>
				{:else}
					{#await import('$lib/components/CodeEditor.svelte')}
						<p class="muted flex items-center gap-2 py-6 text-[12px]"><span class="spinner"></span> Loading editor…</p>
					{:then mod}
						{@const Editor = mod.default}
						<div class="panel-2 overflow-hidden">
							<Editor bind:value={jsonText} onchange={onJSONChange} />
						</div>
					{/await}
					{#if jsonError}
						<p
							class="mt-2 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] px-2 py-1.5 text-[12px] text-[var(--danger)]"
						>
							{jsonError}
						</p>
					{/if}
					<div class="mt-3 flex items-center justify-between gap-2">
						<p class="hint">The server validates the route again and requires a fresh preview.</p>
						<button class="btn btn-primary" onclick={submitJSON} disabled={!parsed || !!jsonError || !editable}>
							<Save size={13} /> Preview changes
						</button>
					</div>
				{/if}
			</div>
		{:else}
			<CaddyfilePreview route={draft ?? view.route} />
		{/if}
	{:else}
		<div class="panel p-4">
			<p class="label">Route object</p>
			<pre class="mono max-h-[60vh] overflow-auto text-[11px]">{jsonText}</pre>
		</div>
	{/if}
{:else}
	<div class="panel px-4 py-6 text-[12px] text-[var(--muted)]">
		Route not found. <a class="link" href="/">Back to the dashboard.</a>
	</div>
{/if}

<PreviewDialog
	preview={previewData}
	open={previewOpen}
	{busy}
	onclose={() => (previewOpen = false)}
	onapply={apply}
/>
