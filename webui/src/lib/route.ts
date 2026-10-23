import type { Spec } from './types';

export function emptySpec(): Spec {
	return {
		host: '',
		path: '',
		upstream: '',
		upstream_tls: false,
		tls_server_name: '',
		insecure_skip_verify: false,
		strip_prefix: '',
		request_set: {},
		request_delete: [],
		response_set: {},
		response_delete: [],
		response_deferred: false,
		extra_tls: {}
	};
}

export function specFromRoute(route: any): Spec {
	const sp = emptySpec();
	if (!route) return sp;
	sp.id = route['@id'] ?? '';
	const match = Array.isArray(route.match) ? route.match[0] ?? {} : {};
	sp.host = Array.isArray(match.host) ? match.host[0] ?? '' : '';
	sp.path = Array.isArray(match.path) ? match.path[0] ?? '' : '';
	for (const h of Array.isArray(route.handle) ? route.handle : []) {
		if (h?.handler === 'rewrite') {
			sp.strip_prefix = h.strip_path_prefix ?? '';
		} else if (h?.handler === 'headers') {
			sp.request_set = toSets(h.request?.set);
			sp.request_delete = toStrings(h.request?.delete);
			sp.response_set = toSets(h.response?.set);
			sp.response_delete = toStrings(h.response?.delete);
			sp.response_deferred = !!h.response?.deferred;
		} else if (h?.handler === 'reverse_proxy') {
			sp.upstream = h.upstreams?.[0]?.dial ?? '';
			if (h.transport) {
				sp.upstream_tls = true;
				sp.tls_server_name = h.transport.tls?.server_name ?? '';
				sp.insecure_skip_verify = !!h.transport.tls?.insecure_skip_verify;
				sp.extra_tls = {};
				for (const [k, v] of Object.entries(h.transport.tls ?? {})) {
					if (k !== 'server_name' && k !== 'insecure_skip_verify') sp.extra_tls[k] = v;
				}
			}
		}
	}
	return sp;
}

function toSets(value: any): Record<string, string[]> {
	const out: Record<string, string[]> = {};
	if (!value || typeof value !== 'object') return out;
	for (const [k, v] of Object.entries(value)) {
		if (Array.isArray(v)) out[k] = v.map(String);
		else if (v != null) out[k] = [String(v)];
	}
	return out;
}

function toStrings(value: any): string[] {
	return Array.isArray(value) ? value.map(String) : [];
}

export function parseHeaderLines(text: string): Record<string, string[]> {
	const out: Record<string, string[]> = {};
	for (const raw of text.split('\n')) {
		const line = raw.trim();
		if (!line || line.startsWith('#')) continue;
		const i = line.indexOf('=');
		if (i <= 0) continue;
		const name = line.slice(0, i).trim();
		const value = line.slice(i + 1).trim();
		if (!name) continue;
		(out[name] ||= []).push(value);
	}
	return out;
}

export function formatHeaderLines(map: Record<string, string[]> | undefined): string {
	if (!map) return '';
	return Object.entries(map)
		.flatMap(([name, values]) => (values.length ? values : ['']).map((v) => `${name}=${v}`))
		.join('\n');
}

export function parseNames(text: string): string[] {
	return text
		.split(/[\s,]+/)
		.map((s) => s.trim())
		.filter(Boolean);
}

export function formatNames(names: string[] | undefined): string {
	return (names ?? []).join(', ');
}

export function pathMatcher(prefix: string): string {
	if (!prefix) return '';
	if (prefix.includes('*')) return prefix;
	return prefix.endsWith('/') ? `${prefix}*` : `${prefix}/*`;
}

export function stripValue(path: string): string {
	return path.replace(/\*$/, '');
}

export function specToJSON(sp: Spec) {
	return {
		'@id': sp.id,
		match: [{ host: [sp.host], ...(sp.path ? { path: [sp.path] } : {}) }],
		handle: [
			...(sp.strip_prefix ? [{ handler: 'rewrite', strip_path_prefix: sp.strip_prefix }] : []),
			{
				handler: 'reverse_proxy',
				upstreams: [{ dial: sp.upstream }],
				...(sp.upstream_tls
					? {
							transport: {
								protocol: 'http',
								tls: {
									...(sp.tls_server_name ? { server_name: sp.tls_server_name } : {}),
									...(sp.insecure_skip_verify ? { insecure_skip_verify: true } : {})
								}
							}
						}
					: {})
			}
		],
		terminal: true
	};
}
