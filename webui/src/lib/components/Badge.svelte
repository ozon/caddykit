<script lang="ts">
	let { status, label = '' }: { status: string; label?: string } = $props();

	const meta: Record<string, { text: string; cls: string }> = {
		managed_sync: { text: 'synced', cls: 'badge-ok' },
		synced: { text: 'synced', cls: 'badge-ok' },
		externally_modified: { text: 'modified', cls: 'badge-warn' },
		missing_restoreable: { text: 'missing', cls: 'badge-warn' },
		foreign: { text: 'foreign', cls: '' },
		self: { text: 'system', cls: 'badge-info' },
		dup_id: { text: 'dup @id', cls: 'badge-danger' },
		op_unclear: { text: 'unclear', cls: 'badge-danger' },
		foreign_collision: { text: 'collision', cls: 'badge-danger' },
		missing_context: { text: 'no context', cls: 'badge-danger' },
		blocked: { text: 'blocked', cls: 'badge-danger' }
	};
	const m = $derived(meta[status] ?? { text: status, cls: '' });
</script>

<span class="badge {m.cls}" title={status}>
	<span class="badge-dot"></span>{label || m.text}
</span>
