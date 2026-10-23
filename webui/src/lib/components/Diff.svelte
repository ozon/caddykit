<script lang="ts">
	import type { Change } from '$lib/types';
	import { jsonPreview } from '$lib/api';

	let { changes = [], compact = false }: { changes?: Change[]; compact?: boolean } = $props();

	const opMeta: Record<string, { mark: string; cls: string }> = {
		add: { mark: '+', cls: 'diff-add' },
		remove: { mark: '−', cls: 'diff-remove' },
		replace: { mark: '~', cls: 'diff-replace' }
	};
</script>

{#if changes.length === 0}
	<p class="hint py-2">No changes.</p>
{:else}
	<div class="flex flex-col gap-0.5 overflow-auto" class:max-h-80={compact}>
		{#each changes as change}
			{@const m = opMeta[change.op] ?? { mark: '?', cls: '' }}
			<div class="diff-row {m.cls}">
				<span class="select-none">{m.mark}</span>
				<span>
					<span class="opacity-70">{change.path || '/'}</span>
					{#if change.op === 'replace'}
						<span class="opacity-60">: </span>{jsonPreview(change.before, 120)}<span class="opacity-60"> → </span>{jsonPreview(change.after, 120)}
					{:else if change.op === 'add'}
						<span class="opacity-60"> = </span>{jsonPreview(change.after, 160)}
					{:else}
						<span class="opacity-60"> = </span>{jsonPreview(change.before, 160)}
					{/if}
				</span>
			</div>
		{/each}
	</div>
{/if}
