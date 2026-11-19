<script lang="ts" module>
	export type Crumb = { label: string; href?: string };
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';

	let { crumbs, actions }: { crumbs: Crumb[]; actions?: Snippet } = $props();
</script>

<header class="top-bar">
	<nav aria-label="Breadcrumb">
		<ol class="crumbs">
			{#each crumbs as crumb, i (i)}
				<li class="crumb">
					{#if i > 0}
						<span class="separator" aria-hidden="true">›</span>
					{/if}
					{#if i === crumbs.length - 1}
						<span class="current" aria-current="page">{crumb.label}</span>
					{:else if crumb.href}
						<a class="link" href={crumb.href}>{crumb.label}</a>
					{:else}
						<span class="link">{crumb.label}</span>
					{/if}
				</li>
			{/each}
		</ol>
	</nav>
	<div class="actions">
		{@render actions?.()}
	</div>
</header>

<style>
	.top-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-4);
		height: 40px;
		box-sizing: border-box;
		padding: 0 var(--space-4);
		border-bottom: 1px solid var(--border-subtle);
	}

	.crumbs {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.crumb {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		min-width: 0;
	}

	.separator {
		color: var(--text-tertiary);
	}

	.link,
	.current {
		overflow: hidden;
		font-size: var(--text-base);
		line-height: var(--text-base-leading);
		white-space: nowrap;
		text-overflow: ellipsis;
	}

	.link {
		color: var(--text-secondary);
		text-decoration: none;
	}

	.link:hover {
		color: var(--text-primary);
	}

	.current {
		color: var(--text-primary);
	}

	.actions {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin-left: auto;
	}
</style>
