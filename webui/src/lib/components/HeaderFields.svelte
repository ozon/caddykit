<script lang="ts">
	import { formatHeaderLines, formatNames, parseHeaderLines, parseNames } from '$lib/route';
	import { isObject } from '$lib/route-edit';
	import GenericFields from './GenericFields.svelte';

	let {
		handler,
		side,
		readonly = false
	}: {
		handler: any;
		side: 'request' | 'response';
		readonly?: boolean;
	} = $props();

	const obj = $derived(isObject(handler[side]) ? handler[side] : null);
	const isResponse = $derived(side === 'response');

	let setText = $state('');
	let deleteText = $state('');
	let lastObj: any = null;

	$effect(() => {
		if (obj !== lastObj) {
			lastObj = obj;
			setText = formatHeaderLines(obj?.set);
			deleteText = formatNames(obj?.delete);
		}
	});

	function ensure(): any {
		if (!isObject(handler[side])) handler[side] = {};
		return handler[side];
	}

	function cleanup() {
		if (isObject(handler[side]) && Object.keys(handler[side]).length === 0) delete handler[side];
	}

	function setSet(text: string) {
		const data = ensure();
		const parsed = parseHeaderLines(text);
		if (Object.keys(parsed).length) data.set = parsed;
		else delete data.set;
		cleanup();
	}

	function setDelete(text: string) {
		const data = ensure();
		const names = parseNames(text);
		if (names.length) data.delete = names;
		else delete data.delete;
		cleanup();
	}

	function setDeferred(on: boolean) {
		const data = ensure();
		if (on) data.deferred = true;
		else delete data.deferred;
		cleanup();
	}
</script>

<div class="panel-2 p-3">
	<p class="label">{isResponse ? 'Response headers' : 'Request headers'} — set</p>
	<textarea
		class="input"
		rows="3"
		spellcheck="false"
		placeholder="Name=Value per line"
		disabled={readonly}
		bind:value={setText}
		oninput={() => setSet(setText)}
	></textarea>
	<p class="label mt-2">{isResponse ? 'Response headers' : 'Request headers'} — delete</p>
	<input
		class="input mono"
		placeholder="X-Old-Header, X-Trace"
		spellcheck="false"
		disabled={readonly}
		bind:value={deleteText}
		oninput={() => setDelete(deleteText)}
	/>
	{#if isResponse}
		<label class="mt-2 flex items-center gap-2 text-[12px]">
			<input
				type="checkbox"
				checked={!!obj?.deferred}
				disabled={readonly}
				onchange={(e) => setDeferred(e.currentTarget.checked)}
			/>
			Deferred (after the response is generated)
		</label>
	{/if}
	{#if obj && !readonly}
		<details class="mt-2">
			<summary class="muted cursor-pointer text-[11px]">Advanced header fields</summary>
			<div class="mt-1">
				<GenericFields value={obj} exclude={['set', 'delete', 'deferred']} />
			</div>
		</details>
	{/if}
</div>
