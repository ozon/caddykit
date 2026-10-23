import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import svelte from 'eslint-plugin-svelte';
import globals from 'globals';

export default tseslint.config(
	{ ignores: ['.svelte-kit/**', 'build/**', 'node_modules/**'] },
	js.configs.recommended,
	...tseslint.configs.recommended,
	...svelte.configs['flat/recommended'],
	{
		languageOptions: {
			globals: { ...globals.browser, ...globals.node }
		},
		rules: {
			'@typescript-eslint/no-explicit-any': 'off',
			'no-undef': 'off',
			// We serve the app at the origin root; resolve() is only needed with a base path.
			'svelte/no-navigation-without-resolve': 'off',
			// Keyed each blocks are an optimisation; several lists are tiny and static.
			'svelte/require-each-key': 'off'
		}
	},
	{
		files: ['**/*.ts', '**/*.svelte.ts'],
		languageOptions: { parser: tseslint.parser }
	},
	{
		files: ['**/*.svelte'],
		languageOptions: {
			parserOptions: { parser: tseslint.parser }
		}
	}
);
