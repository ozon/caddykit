export type Json = any;

export function isObject(v: any): v is Record<string, any> {
	return v !== null && typeof v === 'object' && !Array.isArray(v);
}

export function clone<T>(v: T): T {
	return JSON.parse(JSON.stringify(v)) as T;
}

export function defaultRoute(): Json {
	return {
		match: [{ host: [''] }],
		handle: [{ handler: 'reverse_proxy', upstreams: [{ dial: '' }] }],
		terminal: true
	};
}

export function defaultNestedRoute(): Json {
	return {
		handle: [{ handler: 'reverse_proxy', upstreams: [{ dial: '' }] }]
	};
}

export const HANDLER_KINDS = [
	'reverse_proxy',
	'headers',
	'rewrite',
	'encode',
	'subroute',
	'static_response',
	'file_server'
];

export function newHandler(kind: string): Json {
	switch (kind) {
		case 'reverse_proxy':
			return { handler: 'reverse_proxy', upstreams: [{ dial: '' }] };
		case 'headers':
			return { handler: 'headers', response: { set: {} } };
		case 'rewrite':
			return { handler: 'rewrite', strip_path_prefix: '' };
		case 'encode':
			return { handler: 'encode', encodings: { gzip: {}, zstd: {} }, prefer: ['gzip', 'zstd'] };
		case 'subroute':
			return { handler: 'subroute', routes: [defaultNestedRoute()] };
		case 'static_response':
			return { handler: 'static_response', status_code: 503, body: '' };
		case 'file_server':
			return { handler: 'file_server' };
		default:
			return { handler: '' };
	}
}

export function splitList(text: string): string[] {
	return text
		.split(/[,\s]+/)
		.map((s) => s.trim())
		.filter(Boolean);
}

export function formatList(v: any): string {
	return Array.isArray(v) ? v.filter((x) => typeof x === 'string').join(', ') : '';
}

export function stringArray(v: any): string[] {
	return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [];
}

export function setStringField(obj: Json, key: string, value: string, keepEmpty = false) {
	if (value === '' && !keepEmpty) delete obj[key];
	else obj[key] = value;
}

export function setNumberField(obj: Json, key: string, raw: string) {
	const s = raw.trim();
	if (!s) {
		delete obj[key];
		return;
	}
	const n = Number(s);
	obj[key] = Number.isFinite(n) ? n : s;
}

export function setBoolField(obj: Json, key: string, value: boolean) {
	if (value) obj[key] = true;
	else delete obj[key];
}

export function ensureObject(parent: Json, key: string): Json {
	if (!isObject(parent[key])) parent[key] = {};
	return parent[key];
}

export function ensureArray(parent: Json, key: string): Json[] {
	if (!Array.isArray(parent[key])) parent[key] = [];
	return parent[key];
}

export function matchObject(route: Json): Json | null {
	return Array.isArray(route?.match) && isObject(route.match[0]) ? route.match[0] : null;
}

export function ensureMatch(route: Json): Json {
	if (!Array.isArray(route.match)) route.match = [];
	if (!isObject(route.match[0])) route.match[0] = {};
	return route.match[0];
}

export function setHosts(route: Json, text: string) {
	const m = ensureMatch(route);
	const hosts = splitList(text);
	if (hosts.length) m.host = hosts;
	else delete m.host;
}

export function setPaths(route: Json, text: string) {
	const m = ensureMatch(route);
	const paths = splitList(text);
	if (paths.length) m.path = paths;
	else delete m.path;
}

export function hostText(route: Json): string {
	return formatList(matchObject(route)?.host);
}

export function pathText(route: Json): string {
	return formatList(matchObject(route)?.path);
}

export function setHeaders(obj: Json, key: string, text: string) {
	const out: Record<string, string[]> = {};
	for (const raw of text.split('\n')) {
		const line = raw.trim();
		if (!line || line.startsWith('#')) continue;
		const i = line.indexOf('=');
		if (i <= 0) continue;
		const name = line.slice(0, i).trim();
		const value = line.slice(i + 1).trim();
		if (name) (out[name] ||= []).push(value);
	}
	if (Object.keys(out).length) obj[key] = out;
	else delete obj[key];
}

export function headersText(obj: Json, key: string): string {
	const map = obj?.[key];
	if (!isObject(map)) return '';
	return Object.entries(map)
		.flatMap(([name, values]) => {
			const list = Array.isArray(values) ? values : [values];
			return list.length ? list.map((v) => `${name}=${String(v)}`) : [`${name}=`];
		})
		.join('\n');
}

function validHeaderName(name: string): boolean {
	return /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(name);
}

function validHeaderValue(value: string): boolean {
	return !/[\r\n\0]/.test(value);
}

export function validateRoute(route: Json): string[] {
	const errors: string[] = [];
	walkRoute(route, 'route', 0, errors);
	return errors;
}

function walkRoute(route: Json, path: string, depth: number, errors: string[]) {
	if (depth > 50) {
		errors.push(`${path}: nesting is too deep`);
		return;
	}
	if (!isObject(route)) {
		errors.push(`${path}: route must be an object`);
		return;
	}
	if ('@id' in route && typeof route['@id'] !== 'string') {
		errors.push(`${path}.@id must be a string`);
	}
	if (route.match !== undefined) {
		if (!Array.isArray(route.match)) {
			errors.push(`${path}.match must be an array`);
		} else {
			route.match.forEach((m: any, i: number) => {
				const mp = `${path}.match[${i}]`;
				if (!isObject(m)) {
					errors.push(`${mp} must be an object`);
					return;
				}
				for (const key of ['host', 'path', 'method']) {
					if (m[key] !== undefined && !Array.isArray(m[key])) errors.push(`${mp}.${key} must be an array`);
				}
				if (isObject(m.header)) {
					for (const [name, values] of Object.entries(m.header)) {
						if (!validHeaderName(name)) errors.push(`${mp}.header has an invalid name "${name}"`);
						if (!Array.isArray(values) || values.some((v) => typeof v !== 'string' || !validHeaderValue(v))) {
							errors.push(`${mp}.header.${name} must be an array of valid strings`);
						}
					}
				}
			});
		}
	}
	if (!Array.isArray(route.handle) || route.handle.length === 0) {
		errors.push(`${path}.handle must be a non-empty array`);
		return;
	}
	route.handle.forEach((h: any, i: number) => {
		const hp = `${path}.handle[${i}]`;
		if (!isObject(h)) {
			errors.push(`${hp} must be an object`);
			return;
		}
		const name = h.handler;
		if (typeof name !== 'string' || !name.trim()) {
			errors.push(`${hp}.handler is required`);
			return;
		}
		if (name === 'reverse_proxy') {
			if (!Array.isArray(h.upstreams) || h.upstreams.length === 0) {
				errors.push(`${hp}.upstreams must contain at least one upstream`);
			} else {
				h.upstreams.forEach((u: any, j: number) => {
					if (!isObject(u) || typeof u.dial !== 'string' || !u.dial.trim() || /\s/.test(u.dial)) {
						errors.push(`${hp}.upstreams[${j}].dial must be a host:port without spaces`);
					}
				});
			}
			if (h.transport !== undefined) {
				if (!isObject(h.transport)) errors.push(`${hp}.transport must be an object`);
				else if (h.transport.tls !== undefined && !isObject(h.transport.tls)) {
					errors.push(`${hp}.transport.tls must be an object`);
				}
			}
		} else if (name === 'headers') {
			validateHeaders(h.request, `${hp}.request`, errors);
			validateHeaders(h.response, `${hp}.response`, errors);
			if (h.request !== undefined && !isObject(h.request)) errors.push(`${hp}.request must be an object`);
			if (h.response !== undefined && !isObject(h.response)) errors.push(`${hp}.response must be an object`);
		} else if (name === 'subroute') {
			if (!Array.isArray(h.routes)) {
				errors.push(`${hp}.routes must be an array`);
			} else {
				h.routes.forEach((nested: any, j: number) => walkRoute(nested, `${hp}.routes[${j}]`, depth + 1, errors));
			}
		} else if (name === 'encode') {
			if (!isObject(h.encodings) || Object.keys(h.encodings).length === 0) {
				errors.push(`${hp}.encodings must contain at least one encoding`);
			}
			if (h.minimum_length !== undefined && typeof h.minimum_length !== 'number') {
				errors.push(`${hp}.minimum_length must be a number`);
			}
		} else if (name === 'static_response') {
			if (h.status_code !== undefined) {
				const n = Number(h.status_code);
				if (!Number.isInteger(n) || n < 100 || n > 599) errors.push(`${hp}.status_code must be 100..599`);
			}
			if (h.body !== undefined && typeof h.body !== 'string') errors.push(`${hp}.body must be a string`);
		} else if (name === 'rewrite') {
			if (h.strip_path_prefix !== undefined && typeof h.strip_path_prefix !== 'string') {
				errors.push(`${hp}.strip_path_prefix must be a string`);
			}
		}
	});
	if (route.terminal !== undefined && typeof route.terminal !== 'boolean') {
		errors.push(`${path}.terminal must be a boolean`);
	}
}

function validateHeaders(obj: any, path: string, errors: string[]) {
	if (obj === undefined) return;
	if (!isObject(obj)) {
		errors.push(`${path} must be an object`);
		return;
	}
	for (const key of ['set', 'add']) {
		if (obj[key] === undefined) continue;
		if (!isObject(obj[key])) {
			errors.push(`${path}.${key} must be an object`);
			continue;
		}
		for (const [name, values] of Object.entries(obj[key])) {
			if (!validHeaderName(name)) errors.push(`${path}.${key} has an invalid name "${name}"`);
			if (!Array.isArray(values) || values.some((v) => typeof v !== 'string' || !validHeaderValue(v))) {
				errors.push(`${path}.${key}.${name} must be an array of valid strings`);
			}
		}
	}
	if (obj.delete !== undefined) {
		if (!Array.isArray(obj.delete) || obj.delete.some((v) => typeof v !== 'string' || !validHeaderName(v))) {
			errors.push(`${path}.delete must be an array of header names`);
		}
	}
	if (obj.deferred !== undefined && typeof obj.deferred !== 'boolean') {
		errors.push(`${path}.deferred must be a boolean`);
	}
}
