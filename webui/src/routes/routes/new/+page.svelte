<script lang="ts">
	import { goto } from '$app/navigation';
	import { applyPreview, preview, routeHref } from '$lib/api';
	import type { Preview } from '$lib/types';
	import { toast } from '$lib/store.svelte';
	import { parseHeaderLines, pathMatcher, specToJSON, stripValue } from '$lib/route';
	import Diff from '$lib/components/Diff.svelte';
	import CaddyfilePreview from '$lib/components/CaddyfilePreview.svelte';
	import { ArrowLeft, Save, Sparkles } from '@lucide/svelte';

	let host = $state('');
	let upstream = $state('');
	let pathPrefix = $state('');
	let strip = $state(false);
	let upstreamTLS = $state(false);
	let tlsName = $state('');
	let insecure = $state(false);
	let reqSetText = $state('');
	let respSetText = $state('');

	let previewData = $state<Preview | null>(null);
	let previewError = $state('');
	let previewing = $state(false);
	let applying = $state(false);
	let previewView = $state<'diff' | 'caddyfile'>('diff');
	let seq = 0;

	function buildSpec() {
		return {
			host: host.trim(),
			path: pathMatcher(pathPrefix.trim()),
			upstream: upstream.trim(),
			upstream_tls: upstreamTLS,
			tls_server_name: tlsName.trim(),
			insecure_skip_verify: insecure,
			strip_prefix: strip ? stripValue(pathPrefix.trim()) : '',
			request_set: parseHeaderLines(reqSetText),
			request_delete: [],
			response_set: parseHeaderLines(respSetText),
			response_delete: [],
			response_deferred: false
		};
	}

	$effect(() => {
		const my = ++seq;
		const sp = buildSpec();
		if (!sp.host || !sp.upstream) {
			previewData = null;
			previewError = '';
			previewing = false;
			return;
		}
		const timer = window.setTimeout(async () => {
			previewing = true;
			previewError = '';
			try {
				const p = await preview('create', { spec: sp });
				if (my === seq) previewData = p;
			} catch (err) {
				if (my === seq) {
					previewData = null;
					previewError = err instanceof Error ? err.message : 'Preview failed';
				}
			} finally {
				if (my === seq) previewing = false;
			}
		}, 450);
		return () => window.clearTimeout(timer);
	});

	async function apply() {
		if (!previewData || applying) return;
		applying = true;
		try {
			const res = await applyPreview(previewData);
			toast(`Created ${res.route_id ?? 'route'}`);
			await goto(routeHref(res.route_id ?? host));
		} catch (err: any) {
			if (err?.code === 'PREVIEW_STALE') {
				previewData = await preview('create', { spec: buildSpec() });
				toast('Route list changed — preview refreshed');
			} else {
				toast(err instanceof Error ? err.message : 'Apply failed');
			}
		} finally {
			applying = false;
		}
	}

	function jsonText() {
		return JSON.stringify(specToJSON(buildSpec() as any), null, 2);
	}
</script>

<svelte:head><title>New route · devproxy</title></svelte:head>

<div class="mb-4 flex items-center justify-between gap-3">
	<div>
		<a href="/" class="muted flex items-center gap-1 text-[11.5px] hover:text-[var(--fg)]"
			><ArrowLeft size={12} /> All routes</a
		>
		<h1 class="mt-1 text-[18px] font-semibold tracking-tight">New route</h1>
	</div>
	<div class="flex items-center gap-2">
		<button class="btn" onclick={() => goto('/routes/new/advanced')}>Advanced</button>
		<button class="btn" onclick={() => goto('/routes/new/template')}>
			<Sparkles size={13} /> From template
		</button>
	</div>
</div>

<div class="grid items-start gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
	<form
		class="panel p-4"
		onsubmit={(e) => {
			e.preventDefault();
			}}
	>
		<div class="grid gap-3 sm:grid-cols-2">
			<div>
				<label class="label" for="host">Domain</label>
				<input id="host" bind:value={host} class="input mono" placeholder="app.example.com" spellcheck="false" />
			</div>
			<div>
				<label class="label" for="upstream">Upstream</label>
				<input id="upstream" bind:value={upstream} class="input mono" placeholder="127.0.0.1:3000" spellcheck="false" />
			</div>
		</div>

		<div class="mt-3 grid gap-3 sm:grid-cols-2">
			<div>
				<label class="label" for="path">Path prefix <span class="faint normal-case">(optional)</span></label>
				<input id="path" bind:value={pathPrefix} class="input mono" placeholder="/api" spellcheck="false" />
			</div>
			<div class="flex flex-col justify-end gap-1.5 pb-0.5">
				<label class="flex items-center gap-2 text-[12px]">
					<input type="checkbox" bind:checked={strip} /> Strip the path prefix
				</label>
				<label class="flex items-center gap-2 text-[12px]">
					<input type="checkbox" bind:checked={upstreamTLS} /> HTTPS to upstream
				</label>
			</div>
		</div>

		{#if upstreamTLS}
			<div class="panel-2 mt-3 grid gap-3 p-3 sm:grid-cols-2">
				<div>
					<label class="label" for="tlsname">TLS server name</label>
					<input id="tlsname" bind:value={tlsName} class="input mono" placeholder="derived from upstream" spellcheck="false" />
				</div>
				<label class="flex items-end gap-2 pb-1.5 text-[12px] text-[var(--warn)]">
					<input type="checkbox" bind:checked={insecure} /> Skip certificate verification
				</label>
				{#if insecure}
					<p class="hint text-[var(--warn)] sm:col-span-2">
						Requires the server to run with <span class="mono">--allow-insecure</span>. Never use this in production.
					</p>
				{/if}
			</div>
		{/if}

		<div class="panel-2 mt-3 p-3">
			<p class="label">Request headers</p>
			<textarea
				bind:value={reqSetText}
				class="input"
				rows="3"
				spellcheck="false"
				placeholder="X-Forwarded-Host=app.example.com&#10;Authorization=Bearer …"
			></textarea>
			<p class="hint mt-1">One <span class="mono">Name=Value</span> per line. Values are redacted in previews.</p>
		</div>

		<div class="panel-2 mt-3 p-3">
			<p class="label">Response headers</p>
			<textarea
				bind:value={respSetText}
				class="input"
				rows="3"
				spellcheck="false"
				placeholder="X-Frame-Options=DENY"
			></textarea>
			<p class="hint mt-1">One <span class="mono">Name=Value</span> per line.</p>
		</div>

		<details class="mt-3">
			<summary class="muted cursor-pointer text-[11.5px]">Route JSON</summary>
			<pre class="panel-2 mono mt-2 max-h-56 overflow-auto p-3 text-[11px]">{jsonText()}</pre>
		</details>
	</form>

	<div class="lg:sticky lg:top-16">
		<div class="panel flex min-h-[300px] flex-col">
			<div class="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] px-4 py-2.5">
				<div>
					<p class="label mb-0">Live preview</p>
					<p class="faint mono mt-0.5 text-[10px]">read-modify-write · If-Match</p>
				</div>
				<div class="flex items-center gap-1">
					{#if previewing}<span class="spinner"></span>{/if}
					<button
						class="btn btn-sm {previewView === 'diff'
							? 'border-[var(--accent)] text-[var(--accent)]'
							: ''}"
						onclick={() => (previewView = 'diff')}>Diff</button
					>
					<button
						class="btn btn-sm {previewView === 'caddyfile'
							? 'border-[var(--accent)] text-[var(--accent)]'
							: ''}"
						onclick={() => (previewView = 'caddyfile')}>Caddyfile</button
					>
				</div>
			</div>

			<div class="flex-1 p-3">
				{#if !host || !upstream}
					<p class="muted px-1 py-8 text-center text-[12px]">
						Enter a domain and an upstream to see the preview.
					</p>
				{:else if previewError}
					<p class="border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_7%,var(--panel))] px-2 py-1.5 text-[12px] text-[var(--danger)]">
						{previewError}
					</p>
				{:else if previewData}
					{#if previewData.blocked}
						<div class="mb-2 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] px-2 py-1.5 text-[12px]">
							<span class="mono text-[var(--danger)]">{previewData.blocked.code}</span>
							<span class="muted"> — {previewData.blocked.message}</span>
						</div>
					{/if}
					{#if previewData.warnings?.length}
						{#each previewData.warnings as w}
							<p class="mb-1 text-[11.5px] text-[var(--warn)]">{w}</p>
						{/each}
					{/if}
					{#if previewView === 'diff'}
						<Diff changes={previewData.diff} />
					{:else if previewData.route}
						<CaddyfilePreview route={previewData.route} />
					{:else}
						<p class="muted px-1 py-6 text-center text-[12px]">
							No route is available for a Caddyfile preview.
						</p>
					{/if}
				{:else}
					<p class="muted px-1 py-8 text-center text-[12px]">Waiting for a valid route…</p>
				{/if}
			</div>

			<div class="flex items-center justify-between gap-2 border-t border-[var(--line)] px-4 py-3">
				<span class="faint text-[10.5px]">Review the diff before applying.</span>
				<button
					class="btn btn-primary"
					onclick={apply}
					disabled={!previewData || !!previewData.blocked || applying}
				>
					{#if applying}<span class="spinner"></span>{/if}
					<Save size={13} /> Create route
				</button>
			</div>
		</div>
	</div>
</div>
