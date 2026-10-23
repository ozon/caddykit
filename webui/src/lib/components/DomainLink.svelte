<script lang="ts">
	import { ExternalLink } from '@lucide/svelte';
	import { hostUrl } from '$lib/route';

	let {
		host,
		label,
		className = ''
	}: {
		host: string | null | undefined;
		label?: string;
		className?: string;
	} = $props();

	const href = $derived(hostUrl(host));
	const text = $derived(label ?? host ?? '—');
</script>

{#if href}
	<a
		{href}
		target="_blank"
		rel="noopener noreferrer"
		class="group inline-flex min-w-0 items-center gap-1 hover:text-[var(--fg)] {className}"
		title="Open {host} in a new window"
		aria-label="Open {host} in a new window"
		onclick={(e) => e.stopPropagation()}
	>
		<span class="truncate">{text}</span>
		<ExternalLink
			size="1em"
			class="shrink-0 text-[var(--muted)] group-hover:text-[var(--fg)]"
		/>
	</a>
{:else}
	<span class={className}>{text}</span>
{/if}
