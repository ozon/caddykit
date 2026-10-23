import { defineConfig } from 'vite';
import tailwindcss from '@tailwindcss/vite';
import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';

export default defineConfig({
	plugins: [
		tailwindcss(),
		sveltekit({
			adapter: adapter({ fallback: '200.html' }),
			alias: { $lib: 'src/lib' },
			// adapter-static cannot use per-request nonces, so SvelteKit emits
			// hashes for the inline bootstrap script into the fallback HTML.
			csp: {
				mode: 'hash',
				directives: {
					'default-src': ['self'],
					'script-src': ['self'],
					'style-src': ['self', 'unsafe-inline'],
					'img-src': ['self', 'data:'],
					'font-src': ['self'],
					'connect-src': ['self'],
					'frame-ancestors': ['none'],
					'base-uri': ['none'],
					'form-action': ['self']
				}
			}
		})
	]
});
