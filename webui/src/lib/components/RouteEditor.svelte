<script lang="ts">
	import {
		HANDLER_KINDS,
		defaultNestedRoute,
		ensureArray,
		ensureMatch,
		headersText,
		hostText,
		isObject,
		matchObject,
		newHandler,
		pathText,
		setBoolField,
		setHeaders,
		setHosts,
		setNumberField,
		setPaths,
		setStringField,
		splitList
	} from '$lib/route-edit';
	import GenericFields from './GenericFields.svelte';
	import HeaderFields from './HeaderFields.svelte';
	import Self from './RouteEditor.svelte';

	let {
		route = $bindable(),
		depth = 0,
		readonly = false
	}: {
		route: any;
		depth?: number;
		readonly?: boolean;
	} = $props();

	const handlerTypesId = `handler-types-${Math.random().toString(36).slice(2)}`;
	let addKind = $state('reverse_proxy');
	const match = $derived(matchObject(route));
	const multipleMatches = $derived(Array.isArray(route.match) && route.match.length > 1);

	function addHandler() {
		ensureArray(route, 'handle').push(newHandler(addKind));
	}

	function removeHandler(i: number) {
		if (Array.isArray(route.handle)) route.handle.splice(i, 1);
	}

	function moveHandler(i: number, delta: number) {
		if (!Array.isArray(route.handle)) return;
		const j = i + delta;
		if (j < 0 || j >= route.handle.length) return;
		[route.handle[i], route.handle[j]] = [route.handle[j], route.handle[i]];
	}

	function addNested(h: any) {
		ensureArray(h, 'routes').push(defaultNestedRoute());
	}

	function removeNested(h: any, i: number) {
		if (Array.isArray(h.routes)) h.routes.splice(i, 1);
	}

	function moveNested(h: any, i: number, delta: number) {
		if (!Array.isArray(h.routes)) return;
		const j = i + delta;
		if (j < 0 || j >= h.routes.length) return;
		[h.routes[i], h.routes[j]] = [h.routes[j], h.routes[i]];
	}

	function tls(h: any): any {
		return isObject(h.transport) && isObject(h.transport.tls) ? h.transport.tls : null;
	}

	function toggleTLS(h: any, on: boolean) {
		if (on) {
			if (!isObject(h.transport)) h.transport = { protocol: 'http' };
			if (!isObject(h.transport.tls)) h.transport.tls = {};
		} else if (isObject(h.transport)) {
			delete h.transport.tls;
			if (Object.keys(h.transport).length === 0) delete h.transport;
		}
	}

	function addUpstream(h: any) {
		ensureArray(h, 'upstreams').push({ dial: '' });
	}

	function removeUpstream(h: any, i: number) {
		if (Array.isArray(h.upstreams)) h.upstreams.splice(i, 1);
	}

	function hasEncoding(h: any, name: string): boolean {
		return isObject(h.encodings) && name in h.encodings;
	}

	function toggleEncoding(h: any, name: string, on: boolean) {
		if (!isObject(h.encodings)) h.encodings = {};
		if (on) h.encodings[name] = h.encodings[name] ?? {};
		else {
			delete h.encodings[name];
			if (Object.keys(h.encodings).length === 0) delete h.encodings;
		}
	}

	function preferText(h: any): string {
		return Array.isArray(h.prefer) ? h.prefer.filter((x: any) => typeof x === 'string').join(', ') : '';
	}

	function setPrefer(h: any, text: string) {
		const list = splitList(text);
		if (list.length) h.prefer = list;
		else delete h.prefer;
	}

	function setHeaderMatcher(text: string) {
		setHeaders(ensureMatch(route), 'header', text);
	}
</script>

<fieldset class="contents" disabled={readonly}>
	<datalist id={handlerTypesId}>
		{#each HANDLER_KINDS as kind}
			<option value={kind}></option>
		{/each}
		<option value="file_server"></option>
	</datalist>

	<div class="flex flex-col gap-3">
		<div class={depth === 0 ? 'panel p-3' : 'panel-2 p-3'}>
			<div class="flex flex-wrap items-center justify-between gap-2">
				<div class="flex items-center gap-2">
					<span class="label mb-0">Match</span>
					{#if multipleMatches}<span class="badge badge-warn">first set only</span>{/if}
				</div>
				{#if !readonly}
					<div class="flex items-center gap-1">
						{#if match}
							<button type="button" class="btn btn-sm" onclick={() => delete route.match}>Remove</button>
						{:else}
							<button type="button" class="btn btn-sm" onclick={() => ensureMatch(route)}>Add matcher</button>
						{/if}
					</div>
				{/if}
			</div>

			<div class="mt-2 grid gap-2 sm:grid-cols-2">
				<div>
					<label class="label" for={`host-${depth}-${handlerTypesId}`}>Host</label>
					<input
						id={`host-${depth}-${handlerTypesId}`}
						class="input mono"
						value={hostText(route)}
						placeholder={depth === 0 ? 'app.example.com' : 'optional'}
						spellcheck="false"
						disabled={readonly}
						oninput={(e) => setHosts(route, e.currentTarget.value)}
					/>
				</div>
				<div>
					<label class="label" for={`path-${depth}-${handlerTypesId}`}>Path</label>
					<input
						id={`path-${depth}-${handlerTypesId}`}
						class="input mono"
						value={pathText(route)}
						placeholder="/api/*"
						spellcheck="false"
						disabled={readonly}
						oninput={(e) => setPaths(route, e.currentTarget.value)}
					/>
				</div>
			</div>

			<div class="mt-2">
				<label class="label" for={`header-${depth}-${handlerTypesId}`}>Header matcher</label>
				<textarea
					id={`header-${depth}-${handlerTypesId}`}
					class="input"
					rows="2"
					spellcheck="false"
					placeholder="Upgrade=websocket"
					value={headersText(match ?? {}, 'header')}
					disabled={readonly}
					oninput={(e) => setHeaderMatcher(e.currentTarget.value)}
				></textarea>
			</div>

			{#if match}
				<details class="mt-2">
					<summary class="muted cursor-pointer text-[11px]">Advanced match fields</summary>
					<div class="mt-1">
						<GenericFields value={match} exclude={['host', 'path', 'header']} readonly={readonly} />
					</div>
				</details>
			{/if}
		</div>

		<div class="flex flex-col gap-2">
			<div class="flex flex-wrap items-center justify-between gap-2">
				<span class="label mb-0">Handlers</span>
				{#if !readonly}
					<div class="flex items-center gap-1">
						<select class="input h-7 w-auto text-[11px]" bind:value={addKind}>
							{#each HANDLER_KINDS as kind}
								<option value={kind}>{kind}</option>
							{/each}
							<option value="custom">custom</option>
						</select>
						<button type="button" class="btn btn-sm" onclick={addHandler}>Add handler</button>
					</div>
				{/if}
			</div>

			{#each (Array.isArray(route.handle) ? route.handle : []) as h, i (i)}
				<div class="panel-2 p-3">
					<div class="flex flex-wrap items-center gap-2">
						<input
							class="input mono h-7 w-44 text-[11px]"
							list={handlerTypesId}
							value={h.handler ?? ''}
							placeholder="handler"
							spellcheck="false"
							disabled={readonly}
							oninput={(e) => (h.handler = e.currentTarget.value)}
						/>
						{#if !readonly}
							<div class="ml-auto flex items-center gap-0.5">
								<button
									type="button"
									class="btn btn-sm px-1.5"
									title="Move handler up"
									disabled={i === 0}
									onclick={() => moveHandler(i, -1)}>↑</button
								>
								<button
									type="button"
									class="btn btn-sm px-1.5"
									title="Move handler down"
									disabled={i === route.handle.length - 1}
									onclick={() => moveHandler(i, 1)}>↓</button
								>
								<button type="button" class="btn btn-sm btn-danger px-1.5" title="Remove handler" onclick={() => removeHandler(i)}
									>×</button
								>
							</div>
						{/if}
					</div>

					<div class="mt-2">
						{#if h.handler === 'reverse_proxy'}
							<div class="flex flex-col gap-2">
								<p class="label mb-0">Upstreams</p>
								{#each (Array.isArray(h.upstreams) ? h.upstreams : []) as u, j (j)}
									<div class="flex items-center gap-1">
										<input
											class="input mono h-7"
											value={u.dial ?? ''}
											placeholder="127.0.0.1:3000"
											spellcheck="false"
											disabled={readonly}
											oninput={(e) => (u.dial = e.currentTarget.value)}
										/>
										{#if !readonly}
											<button type="button" class="btn btn-sm btn-danger px-1.5" title="Remove upstream" onclick={() => removeUpstream(h, j)}
												>×</button
											>
										{/if}
									</div>
								{/each}
								{#if !readonly}
									<button type="button" class="btn btn-sm w-fit" onclick={() => addUpstream(h)}>Add upstream</button>
								{/if}

								<div class="mt-1 grid gap-2 sm:grid-cols-2">
									<label class="flex items-center gap-2 text-[12px]">
										<input
											type="checkbox"
											checked={!!tls(h)}
											disabled={readonly}
											onchange={(e) => toggleTLS(h, e.currentTarget.checked)}
										/>
										HTTPS to upstream
									</label>
									{#if tls(h)}
										{@const t = tls(h)}
										<input
											class="input mono h-7"
											value={t.server_name ?? ''}
											placeholder="TLS server name"
											spellcheck="false"
											disabled={readonly}
											oninput={(e) => setStringField(t, 'server_name', e.currentTarget.value)}
										/>
										<label class="flex items-center gap-2 text-[12px] text-[var(--warn)]">
											<input
												type="checkbox"
												checked={!!t.insecure_skip_verify}
												disabled={readonly}
												onchange={(e) => setBoolField(t, 'insecure_skip_verify', e.currentTarget.checked)}
											/>
											Skip certificate verification
										</label>
										{#if t.insecure_skip_verify}
											<p class="hint text-[var(--warn)] sm:col-span-2">
												Requires the server to run with <span class="mono">--allow-insecure</span>.
											</p>
										{/if}
										<details class="sm:col-span-2">
											<summary class="muted cursor-pointer text-[11px]">Advanced TLS fields</summary>
											<div class="mt-1">
												<GenericFields
													value={t}
													exclude={['server_name', 'insecure_skip_verify']}
													readonly={readonly}
												/>
											</div>
										</details>
									{/if}
								</div>

								<div>
									<label class="label" for={`flush-${handlerTypesId}-${depth}-${i}`}>flush_interval</label>
									<input
										id={`flush-${handlerTypesId}-${depth}-${i}`}
										class="input mono h-7"
										value={h.flush_interval ?? ''}
										placeholder="-1"
										spellcheck="false"
										disabled={readonly}
										oninput={(e) => setNumberField(h, 'flush_interval', e.currentTarget.value)}
									/>
								</div>

								{#if tls(h)}
									<details>
										<summary class="muted cursor-pointer text-[11px]">Advanced transport fields</summary>
										<div class="mt-1">
											<GenericFields value={h.transport} exclude={['protocol', 'tls']} readonly={readonly} />
										</div>
									</details>
								{/if}
								<details>
									<summary class="muted cursor-pointer text-[11px]">More reverse_proxy fields</summary>
									<div class="mt-1">
										<GenericFields
											value={h}
											exclude={['handler', 'upstreams', 'transport', 'flush_interval']}
											readonly={readonly}
										/>
									</div>
								</details>
							</div>
						{:else if h.handler === 'headers'}
							<div class="flex flex-col gap-2">
								<HeaderFields handler={h} side="request" readonly={readonly} />
								<HeaderFields handler={h} side="response" readonly={readonly} />
								<details>
									<summary class="muted cursor-pointer text-[11px]">More headers fields</summary>
									<div class="mt-1">
										<GenericFields value={h} exclude={['handler', 'request', 'response']} readonly={readonly} />
									</div>
								</details>
							</div>
						{:else if h.handler === 'rewrite'}
							<div class="flex flex-col gap-2">
								<div>
									<label class="label" for={`strip-${handlerTypesId}-${depth}-${i}`}>Strip path prefix</label>
									<input
										id={`strip-${handlerTypesId}-${depth}-${i}`}
										class="input mono h-7"
										value={h.strip_path_prefix ?? ''}
										placeholder="/api"
										spellcheck="false"
										disabled={readonly}
										oninput={(e) => setStringField(h, 'strip_path_prefix', e.currentTarget.value)}
									/>
								</div>
								<details>
									<summary class="muted cursor-pointer text-[11px]">More rewrite fields</summary>
									<div class="mt-1">
										<GenericFields value={h} exclude={['handler', 'strip_path_prefix']} readonly={readonly} />
									</div>
								</details>
							</div>
						{:else if h.handler === 'encode'}
							<div class="flex flex-col gap-2">
								<div class="flex flex-wrap items-center gap-3">
									{#each ['gzip', 'zstd'] as fmt}
										<label class="flex items-center gap-1.5 text-[12px]">
											<input
												type="checkbox"
												checked={hasEncoding(h, fmt)}
												disabled={readonly}
												onchange={(e) => toggleEncoding(h, fmt, e.currentTarget.checked)}
											/>
											{fmt}
										</label>
									{/each}
								</div>
								<div class="grid gap-2 sm:grid-cols-2">
									<div>
										<label class="label" for={`prefer-${handlerTypesId}-${depth}-${i}`}>Prefer order</label>
										<input
											id={`prefer-${handlerTypesId}-${depth}-${i}`}
											class="input mono h-7"
											value={preferText(h)}
											placeholder="gzip, zstd"
											spellcheck="false"
											disabled={readonly}
											oninput={(e) => setPrefer(h, e.currentTarget.value)}
										/>
									</div>
									<div>
										<label class="label" for={`minlen-${handlerTypesId}-${depth}-${i}`}>Minimum length</label>
										<input
											id={`minlen-${handlerTypesId}-${depth}-${i}`}
											class="input mono h-7"
											value={h.minimum_length ?? ''}
											placeholder="512"
											spellcheck="false"
											disabled={readonly}
											oninput={(e) => setNumberField(h, 'minimum_length', e.currentTarget.value)}
										/>
									</div>
								</div>
								{#if isObject(h.encodings)}
									<details>
										<summary class="muted cursor-pointer text-[11px]">Other encodings</summary>
										<div class="mt-1">
											<GenericFields value={h.encodings} exclude={['gzip', 'zstd']} readonly={readonly} />
										</div>
									</details>
								{/if}
								<details>
									<summary class="muted cursor-pointer text-[11px]">More encode fields</summary>
									<div class="mt-1">
										<GenericFields
											value={h}
											exclude={['handler', 'encodings', 'prefer', 'minimum_length']}
											readonly={readonly}
										/>
									</div>
								</details>
							</div>
						{:else if h.handler === 'subroute'}
							<div class="flex flex-col gap-2">
								{#each (Array.isArray(h.routes) ? h.routes : []) as nested, j (j)}
									<div class="border border-[var(--line)] bg-[var(--panel)] p-2">
										<div class="mb-1 flex flex-wrap items-center justify-between gap-2">
											<span class="mono faint text-[10.5px]">nested route {j + 1}</span>
											{#if !readonly}
												<div class="flex items-center gap-0.5">
													<button
														type="button"
														class="btn btn-sm px-1.5"
														title="Move nested route up"
														disabled={j === 0}
														onclick={() => moveNested(h, j, -1)}>↑</button
													>
													<button
														type="button"
														class="btn btn-sm px-1.5"
														title="Move nested route down"
														disabled={j === h.routes.length - 1}
														onclick={() => moveNested(h, j, 1)}>↓</button
													>
													<button
														type="button"
														class="btn btn-sm btn-danger px-1.5"
														title="Remove nested route"
														onclick={() => removeNested(h, j)}>×</button
													>
												</div>
											{/if}
										</div>
										<Self route={nested} depth={depth + 1} {readonly} />
									</div>
								{/each}
								{#if !readonly}
									<button type="button" class="btn btn-sm w-fit" onclick={() => addNested(h)}>Add nested route</button>
								{/if}
								<details>
									<summary class="muted cursor-pointer text-[11px]">More subroute fields</summary>
									<div class="mt-1">
										<GenericFields value={h} exclude={['handler', 'routes']} readonly={readonly} />
									</div>
								</details>
							</div>
						{:else if h.handler === 'static_response'}
							<div class="flex flex-col gap-2">
								<div class="grid gap-2 sm:grid-cols-2">
									<div>
										<label class="label" for={`status-${handlerTypesId}-${depth}-${i}`}>Status code</label>
										<input
											id={`status-${handlerTypesId}-${depth}-${i}`}
											type="number"
											class="input mono h-7"
											value={h.status_code ?? ''}
											disabled={readonly}
											oninput={(e) => setNumberField(h, 'status_code', e.currentTarget.value)}
										/>
									</div>
									<div class="sm:col-span-2">
										<label class="label" for={`body-${handlerTypesId}-${depth}-${i}`}>Body</label>
										<textarea
											id={`body-${handlerTypesId}-${depth}-${i}`}
											class="input"
											rows="3"
											spellcheck="false"
											disabled={readonly}
											value={h.body ?? ''}
											oninput={(e) => (h.body = e.currentTarget.value)}
										></textarea>
									</div>
								</div>
								<details>
									<summary class="muted cursor-pointer text-[11px]">More static_response fields</summary>
									<div class="mt-1">
										<GenericFields value={h} exclude={['handler', 'status_code', 'body']} readonly={readonly} />
									</div>
								</details>
							</div>
						{:else}
							<GenericFields value={h} exclude={['handler']} readonly={readonly} />
						{/if}
					</div>
				</div>
			{:else}
				<p class="muted text-[11.5px]">No handlers yet. Add one above.</p>
			{/each}
		</div>

		<div class="flex items-center gap-2">
			<input
				type="checkbox"
				checked={!!route.terminal}
				disabled={readonly}
				onchange={(e) => setBoolField(route, 'terminal', e.currentTarget.checked)}
			/>
			<span class="text-[12px]">terminal</span>
			<span class="hint">Stop evaluating later top-level routes.</span>
		</div>

		<details>
			<summary class="muted cursor-pointer text-[11px]">Advanced route fields</summary>
			<div class="mt-1">
				<GenericFields
					value={route}
					exclude={['@id', 'match', 'handle', 'terminal']}
					readonly={readonly}
				/>
			</div>
		</details>
	</div>
</fieldset>
