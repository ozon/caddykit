import { goto } from '$app/navigation';
import type { CaddyfilePreview, Preview, Result, Status, UndoInfo } from './types';

let csrf = '';

export class ApiError extends Error {
	status: number;
	code: string;
	blockReason?: string;
	candidates?: string[];

	constructor(status: number, code: string, message: string, blockReason?: string, candidates?: string[]) {
		super(message);
		this.status = status;
		this.code = code;
		this.blockReason = blockReason;
		this.candidates = candidates;
	}
}

export function readCsrfCookie(): string {
	if (typeof document === 'undefined') return '';
	const m = document.cookie.match(/(?:^|; )devproxy_csrf=([^;]+)/);
	return m ? decodeURIComponent(m[1]) : '';
}

export function setCsrf(value: string) {
	csrf = value;
}

export function getCsrf(): string {
	return csrf || readCsrfCookie();
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
	const method = (init.method ?? 'GET').toUpperCase();
	const headers = new Headers(init.headers);
	if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
	if (method !== 'GET' && method !== 'HEAD') headers.set('X-CSRF-Token', getCsrf());
	const res = await fetch(path, { ...init, headers, credentials: 'same-origin' });
	let data: unknown = null;
	const text = await res.text();
	if (text) {
		try {
			data = JSON.parse(text);
		} catch {
			data = { error: text };
		}
	}
	const body = (data ?? {}) as Record<string, unknown>;
	if (res.status === 401) {
		if (typeof window !== 'undefined' && window.location.pathname !== '/login') await goto('/login');
		throw new ApiError(401, 'UNAUTHORIZED', 'Login required');
	}
	if (!res.ok) {
		throw new ApiError(
			res.status,
			String(body.code ?? 'INTERNAL'),
			String(body.error ?? res.statusText),
			body.block_reason ? String(body.block_reason) : undefined,
			Array.isArray(body.candidates) ? (body.candidates as string[]) : undefined
		);
	}
	return data as T;
}

export async function login(password: string): Promise<void> {
	const res = await api<{ csrf: string }>('/api/v1/login', {
		method: 'POST',
		body: JSON.stringify({ password })
	});
	setCsrf(res.csrf);
}

export async function logout(): Promise<void> {
	await api('/api/v1/logout', { method: 'POST', body: '{}' });
	setCsrf('');
	await goto('/login');
}

export async function fetchStatus(): Promise<Status> {
	return api<Status>('/api/v1/status');
}

export async function preview(kind: string, payload: Record<string, unknown>): Promise<Preview> {
	return api<Preview>('/api/v1/routes/preview', {
		method: 'POST',
		body: JSON.stringify({ kind, ...payload })
	});
}

export async function applyPreview(p: Preview): Promise<Result> {
	return api<Result>('/api/v1/routes/apply', {
		method: 'POST',
		body: JSON.stringify(p)
	});
}

export async function resolvePreview(id: string, action: string): Promise<Preview> {
	return api<Preview>('/api/v1/conflicts/resolve', {
		method: 'POST',
		body: JSON.stringify({ id, action })
	});
}

export async function renderCaddyfile(route: unknown): Promise<CaddyfilePreview> {
	return api<CaddyfilePreview>('/api/v1/render/caddyfile', {
		method: 'POST',
		body: JSON.stringify({ route })
	});
}

export async function undoInfo(): Promise<UndoInfo> {
	return api<UndoInfo>('/api/v1/undo');
}

export async function applyUndo(): Promise<Result> {
	return api<Result>('/api/v1/undo', { method: 'POST', body: '{}' });
}

export function routeHref(id: string): string {
	return `/routes/${id.split('/').map(encodeURIComponent).join('/')}`;
}

export function formatTime(value?: string): string {
	if (!value) return '—';
	const d = new Date(value);
	if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return '—';
	return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

export function jsonPreview(value: unknown, max = 240): string {
	let s: string;
	try {
		s = JSON.stringify(value);
	} catch {
		s = String(value);
	}
	if (s === undefined) return '';
	return s.length > max ? `${s.slice(0, max - 3)}...` : s;
}
