<script lang="ts">
	import { isObject } from '$lib/route-edit';

	let {
		value,
		exclude = [],
		depth = 0,
		readonly = false
	}: {
		value: any;
		exclude?: string[];
		depth?: number;
		readonly?: boolean;
	} = $props();

	function addKey(obj: any, raw: string) {
		const key = String(raw ?? '').trim();
		if (!key || key in obj) return false;
		obj[key] = '';
		return true;
	}

	function renameKey(obj: any, oldKey: string, raw: string) {
		const key = String(raw ?? '').trim();
		if (!key || key === oldKey || key in obj) return false;
		const entries = Object.entries(obj);
		for (const k of Object.keys(obj)) delete obj[k];
		for (const [k, v] of entries) obj[k === oldKey ? key : k] = v;
		return true;
	}

	function remove(parent: any, key: string | number) {
		if (Array.isArray(parent)) parent.splice(Number(key), 1);
		else delete parent[key];
	}

	function move(arr: any[], i: number, delta: number) {
		const j = i + delta;
		if (j < 0 || j >= arr.length) return;
		[arr[i], arr[j]] = [arr[j], arr[i]];
	}

	function setChild(parent: any, key: string | number, v: any) {
		if (parent !== null) parent[key] = v;
	}

	function defaultItem(arr: any[]) {
		if (arr.length === 0) return '';
		const first = arr[0];
		if (isObject(first)) return {};
		if (Array.isArray(first)) return [];
		if (typeof first === 'boolean') return false;
		if (typeof first === 'number') return 0;
		return '';
	}

</script>

{#snippet node(val: any, parent: any, key: string | number, level: number)}
	<div class="py-0.5">
		<div class="flex items-start gap-1.5">
			<div class="w-32 shrink-0 pt-0.5 sm:w-40">
				{#if parent === null}
					<span class="mono faint text-[10.5px]">root</span>
				{:else if Array.isArray(parent)}
					<span class="mono faint text-[10.5px]">[{key}]</span>
				{:else}
					<input
						class="input mono h-7 px-1.5 text-[11px]"
						value={key}
						disabled={readonly}
						onchange={(e) => {
							if (!renameKey(parent, String(key), e.currentTarget.value)) e.currentTarget.value = String(key);
						}}
					/>
				{/if}
			</div>

			<div class="min-w-0 flex-1">
				{#if isObject(val)}
					<details open={level < 2}>
						<summary class="cursor-pointer text-[11px] text-[var(--info)]">
							object · {Object.keys(val).length} key{Object.keys(val).length === 1 ? '' : 's'}
						</summary>
						<div class="mt-1 ml-1 border-l border-[var(--line)] pl-2">
							{@render objectChildren(val, level + 1, [])}
						</div>
					</details>
				{:else if Array.isArray(val)}
					<details open={level < 2}>
						<summary class="cursor-pointer text-[11px] text-[var(--info)]">
							array · {val.length} item{val.length === 1 ? '' : 's'}
						</summary>
						<div class="mt-1 ml-1 border-l border-[var(--line)] pl-2">
							{@render arrayChildren(val, level + 1)}
						</div>
					</details>
				{:else if typeof val === 'boolean'}
					<label class="flex h-7 items-center gap-2 text-[12px]">
						<input
							type="checkbox"
							checked={val}
							disabled={readonly}
							onchange={(e) => setChild(parent, key, e.currentTarget.checked)}
						/>
						<span class="mono faint text-[10.5px]">{val ? 'true' : 'false'}</span>
					</label>
				{:else if typeof val === 'number'}
					<input
						type="number"
						class="input mono h-7 text-[11px]"
						value={val}
						disabled={readonly}
						oninput={(e) => setChild(parent, key, e.currentTarget.value === '' ? 0 : Number(e.currentTarget.value))}
					/>
				{:else}
					<input
						class="input mono h-7 text-[11px]"
						value={val ?? ''}
						placeholder={val === null || val === undefined ? 'null' : ''}
						disabled={readonly}
						oninput={(e) => setChild(parent, key, e.currentTarget.value)}
					/>
				{/if}
			</div>

			{#if parent !== null && !readonly}
				<div class="flex shrink-0 items-center gap-0.5">
					{#if Array.isArray(parent)}
						<button
							type="button"
							class="btn btn-sm px-1.5"
							title="Move up"
							disabled={Number(key) === 0}
							onclick={() => move(parent, Number(key), -1)}>↑</button
						>
						<button
							type="button"
							class="btn btn-sm px-1.5"
							title="Move down"
							disabled={Number(key) === parent.length - 1}
							onclick={() => move(parent, Number(key), 1)}>↓</button
						>
					{/if}
					<button type="button" class="btn btn-sm btn-danger px-1.5" title="Remove" onclick={() => remove(parent, key)}>×</button>
				</div>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet objectChildren(obj: any, level: number, hidden: string[])}
	{#each Object.entries(obj).filter(([k]) => !hidden.includes(k)) as [k, v] (k)}
		{@render node(v, obj, k, level)}
	{/each}
	{#if !readonly}
		<div class="mt-1 flex items-center gap-1">
			<input
				class="input mono h-7 max-w-48 px-1.5 text-[11px]"
				placeholder="new field"
				onkeydown={(e) => {
					if (e.key === 'Enter') {
						addKey(obj, e.currentTarget.value);
						e.currentTarget.value = '';
					}
				}}
			/>
			<button
				type="button"
				class="btn btn-sm"
				onclick={(e) => {
					const input = e.currentTarget.previousElementSibling as HTMLInputElement | null;
					if (input && addKey(obj, input.value)) input.value = '';
				}}>Add</button
			>
		</div>
	{/if}
{/snippet}

{#snippet arrayChildren(arr: any[], level: number)}
	{#each arr as item, i (i)}
		{@render node(item, arr, i, level)}
	{/each}
	{#if !readonly}
		<button type="button" class="btn btn-sm mt-1" onclick={() => arr.push(defaultItem(arr))}>Add item</button>
	{/if}
{/snippet}

{#if isObject(value)}
	<div class="flex flex-col">
		{@render objectChildren(value, depth, exclude)}
	</div>
{:else if Array.isArray(value)}
	<div class="flex flex-col">
		{@render arrayChildren(value, depth)}
	</div>
{:else}
	<p class="faint mono text-[11px]">{value === null || value === undefined ? 'null' : String(value)}</p>
{/if}
