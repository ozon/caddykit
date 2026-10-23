<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { login } from '$lib/api';

	let password = $state('');
	let error = $state('');
	let busy = $state(false);
	let input = $state<HTMLInputElement>();

	onMount(() => input?.focus());

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (busy || !password) return;
		busy = true;
		error = '';
		try {
			await login(password);
			await goto('/');
		} catch (err) {
			error = err instanceof Error ? err.message : 'Login failed';
			password = '';
			input?.focus();
		} finally {
			busy = false;
		}
	}
</script>

<div class="mx-auto mt-[12vh] w-full max-w-[360px]">
	<div class="mb-6 flex items-center gap-2">
		<span
			class="mono flex h-7 w-7 items-center justify-center border border-[var(--accent)] text-[12px] font-bold text-[var(--accent)]"
			>dp</span
		>
		<div>
			<p class="mono text-[14px] font-semibold leading-none">caddykit</p>
			<p class="faint mono mt-1 text-[10px] tracking-[0.12em] uppercase">caddy route control</p>
		</div>
	</div>

	<form class="panel p-5" onsubmit={submit}>
		<h1 class="text-[15px] font-semibold">Sign in</h1>
		<p class="muted mt-1 text-[12px]">
			Use the start password printed by <span class="mono">caddykit web</span>. It changes every process start.
		</p>

		<div class="mt-5">
			<label class="label" for="password">Start password</label>
			<input
				id="password"
				bind:this={input}
				bind:value={password}
				class="input mono"
				type="password"
				autocomplete="current-password"
				spellcheck="false"
				placeholder="••••••••••••••••••••••••"
			/>
		</div>

		{#if error}
			<p class="mt-3 border border-[color-mix(in_srgb,var(--danger)_35%,var(--line))] bg-[color-mix(in_srgb,var(--danger)_8%,var(--panel))] px-2 py-1.5 text-[12px] text-[var(--danger)]">
				{error}
			</p>
		{/if}

		<button class="btn btn-primary mt-5 w-full" type="submit" disabled={busy || !password}>
			{#if busy}<span class="spinner"></span>{/if}
			Sign in
		</button>
	</form>

	<p class="faint mono mt-4 text-center text-[10.5px]">
		Sessions end with the process · cookie Secure / HttpOnly / SameSite=Strict
	</p>
</div>
