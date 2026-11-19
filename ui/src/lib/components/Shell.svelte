<script lang="ts">
	import type { Snippet } from 'svelte';

	import Sidebar, { type NavItem } from './Sidebar.svelte';
	import TopBar, { type Crumb } from './TopBar.svelte';

	let { nav, crumbs, actions, children }: {
		nav: NavItem[];
		crumbs: Crumb[];
		actions?: Snippet;
		children: Snippet;
	} = $props();
</script>

<div class="shell">
	<Sidebar {nav} />
	<main class="canvas">
		<TopBar {crumbs} {actions} />
		{@render children()}
	</main>
</div>

<style>
	.shell {
		position: fixed;
		inset: 0;
		display: grid;
		grid-template-columns: var(--sidebar-width) 1fr;
		background: var(--surface-frame);
	}

	.canvas {
		margin: var(--canvas-margin) var(--canvas-margin) var(--canvas-margin) 0;
		overflow: auto;
		min-width: 0;
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-lg);
		background: var(--surface-canvas);
		color: var(--text-primary);
		font-family: var(--font-sans);
	}
</style>
