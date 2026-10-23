export type RouteClass = 'FORM_EDITABLE' | 'JSON_ONLY';

export interface Reason {
	code: string;
	message: string;
}

export interface RouteView {
	id: string;
	managed: boolean;
	self: boolean;
	host: string;
	paths: string[] | null;
	upstream: string;
	class: RouteClass;
	reason?: Reason;
	source?: string;
	status: string;
	index: number;
	route?: Record<string, unknown> | null;
}

export interface Change {
	op: 'add' | 'remove' | 'replace';
	path: string;
	before?: unknown;
	after?: unknown;
}

export interface Entry {
	id: string;
	snapshot: Record<string, unknown>;
	anchor: { index: number };
	adopted_at: string;
	source: string;
	template?: { name: string; version?: string };
}

export interface Preview {
	kind: string;
	route_id?: string;
	diff: Change[];
	etag?: string;
	route?: Record<string, unknown>;
	base?: Record<string, unknown>;
	index: number;
	entry?: Entry;
	remove_entry?: boolean;
	local_only?: boolean;
	blocked?: Reason;
	warnings?: string[];
	description: string;
	requirements?: string[];
}

export interface Result {
	kind: string;
	route_id?: string;
	route?: Record<string, unknown>;
	entry?: Entry;
	diff: Change[];
	message: string;
}

export interface Conflict {
	route_id: string;
	kind: string;
	host: string;
	reason: string;
	blocked?: Record<string, Reason>;
	current?: Record<string, unknown>;
	desired?: Record<string, unknown>;
}

export interface Status {
	caddy_ok: boolean;
	caddy_error?: string;
	server: string;
	last_reconcile: string;
	state_routes: number;
	conflicts: number;
	undo_available: boolean;
	domain: string;
	version: string;
	reconcile_interval: string;
}

export interface TemplateParameter {
	name: string;
	type: string;
	required: boolean;
	default?: unknown;
	sensitive?: boolean;
	description?: string;
}

export interface Template {
	schema_version: number;
	id: string;
	name: string;
	description: string;
	parameters: TemplateParameter[] | null;
	requirements: string[] | null;
	route: Record<string, unknown>;
}

export interface CaddyfilePreview {
	caddyfile: string;
	warnings: string[];
}

export interface UndoInfo {
	available: boolean;
	kind?: string;
	description?: string;
	created_at?: string;
}

export interface Spec {
	id?: string;
	host: string;
	path: string;
	upstream: string;
	upstream_tls: boolean;
	tls_server_name: string;
	insecure_skip_verify: boolean;
	strip_prefix: string;
	request_set: Record<string, string[]>;
	request_delete: string[];
	response_set: Record<string, string[]>;
	response_delete: string[];
	response_deferred: boolean;
	extra_tls: Record<string, unknown>;
}
