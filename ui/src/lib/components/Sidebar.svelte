<script lang="ts" module>
	export type NavItem = { label: string; href: string; active: boolean };
</script>

<script lang="ts">
	import Orb from './Orb.svelte';

	let { nav }: { nav: NavItem[] } = $props();
</script>

<aside class="sidebar" aria-label="Sidebar">
	<div class="brand">
		<Orb />
		<span class="wordmark">Moonglass</span>
	</div>
	<nav aria-label="Main">
		<ul class="items">
			{#each nav as item (item.href)}
				<li>
					<a href={item.href} class="item" class:active={item.active} aria-current={item.active ? 'page' : undefined}>
						{item.label}
					</a>
				</li>
			{/each}
		</ul>
	</nav>
</aside>

<style>
	.sidebar {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		padding: var(--space-3) var(--space-2);
		background: var(--surface-frame);
		color: var(--text-primary);
		font-family: var(--font-sans);
	}

	.brand {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: 0 var(--space-2);
	}

	.wordmark {
		font-size: var(--text-base);
		font-weight: 600;
		letter-spacing: var(--text-base-tracking);
		color: var(--text-primary);
	}

	.items {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.item {
		position: relative;
		display: flex;
		align-items: center;
		min-height: 24px;
		padding: 0 var(--space-2);
		border-radius: var(--radius-sm);
		color: var(--text-secondary);
		font-size: var(--text-base);
		line-height: var(--text-base-leading);
		text-decoration: none;
		transition:
			background-color var(--duration-micro) var(--ease-out),
			color var(--duration-micro) var(--ease-out);
	}

	.item:hover {
		background: var(--surface-hover);
		color: var(--text-primary);
	}

	.item.active {
		background: var(--surface-active);
		color: var(--text-primary);
	}

	.item.active::before {
		content: "";
		position: absolute;
		top: var(--space-1);
		bottom: var(--space-1);
		left: 0;
		width: 2px;
		background: var(--accent);
	}

	.item:focus-visible {
		outline: var(--focus-ring-width) solid var(--accent);
		outline-offset: var(--focus-ring-offset);
	}
</style>
