<script lang="ts">
	import { onMount, onDestroy } from 'svelte';

	let {
		value = $bindable(''),
		onchange
	}: { value: string; onchange?: (value: string) => void } = $props();

	let host = $state<HTMLDivElement>();
	let view: any = undefined;

	onMount(async () => {
		const [{ EditorView, keymap, lineNumbers, highlightActiveLine }, { defaultKeymap, history, historyKeymap }, { json, jsonParseLinter }, { linter, lintGutter }, { EditorState }] =
			await Promise.all([
				import('@codemirror/view'),
				import('@codemirror/commands'),
				import('@codemirror/lang-json'),
				import('@codemirror/lint'),
				import('@codemirror/state')
			]);

		const theme = EditorView.theme(
			{
				'&': {
					color: 'var(--fg)',
					backgroundColor: 'transparent',
					fontSize: '12px',
					height: '100%'
				},
				'.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.55', overflow: 'auto' },
				'.cm-content': { caretColor: 'var(--accent)', padding: '10px 0' },
				'.cm-gutters': {
					backgroundColor: 'transparent',
					color: 'var(--faint)',
					border: 'none',
					paddingLeft: '4px'
				},
				'.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--panel-2) 65%, transparent)' },
				'.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--muted)' },
				'.cm-selectionBackground, ::selection': { backgroundColor: 'color-mix(in srgb, var(--accent) 22%, transparent) !important' },
				'.cm-tooltip': {
					backgroundColor: 'var(--panel-2)',
					border: '1px solid var(--line-strong)',
					color: 'var(--fg)',
					fontFamily: 'var(--font-mono)',
					fontSize: '11px'
				},
				'.cm-diagnostic-error': { borderLeftColor: 'var(--danger)' },
				'.cm-lintRange-error': { backgroundImage: 'none', borderBottom: '1px wavy var(--danger)' },
				'&.cm-focused': { outline: 'none' }
			},
			{ dark: true }
		);

		view = new EditorView({
			state: EditorState.create({
				doc: value,
				extensions: [
					lineNumbers(),
					highlightActiveLine(),
					history(),
					json(),
					linter(jsonParseLinter()),
					lintGutter(),
					keymap.of([...defaultKeymap, ...historyKeymap]),
					EditorView.updateListener.of((update: any) => {
						if (update.docChanged) {
							const next = update.state.doc.toString();
							value = next;
							onchange?.(next);
						}
					}),
					theme
				]
			}),
			parent: host!
		});
	});

	$effect(() => {
		if (!view) return;
		const current = view.state.doc.toString();
		if (value !== current) {
			view.dispatch({ changes: { from: 0, to: current.length, insert: value } });
		}
	});

	onDestroy(() => {
		view?.destroy();
		view = undefined;
	});
</script>

<div bind:this={host} class="h-[48vh] min-h-64 overflow-hidden"></div>
