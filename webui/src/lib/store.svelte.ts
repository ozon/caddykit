import type { Conflict, RouteView, Status } from './types';

export const app = $state({
	status: null as Status | null,
	routes: [] as RouteView[],
	conflicts: [] as Conflict[],
	paletteOpen: false,
	toast: '',
	searchFocus: 0,
	theme: 'dark' as 'dark' | 'light'
});

let toastTimer: number | undefined;

export function toast(message: string, ms = 4000) {
	app.toast = message;
	if (toastTimer) window.clearTimeout(toastTimer);
	toastTimer = window.setTimeout(() => {
		if (app.toast === message) app.toast = '';
	}, ms);
}

export function setTheme(theme: 'dark' | 'light') {
	app.theme = theme;
	if (typeof document !== 'undefined') document.documentElement.dataset.theme = theme;
	if (typeof localStorage !== 'undefined') localStorage.setItem('devproxy.theme', theme);
}
