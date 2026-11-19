<script lang="ts">
	import type { Snippet } from 'svelte';

	let { title, summary, status, meta, actions }: {
		title: string;
		summary?: string;
		status?: Snippet;
		meta?: Snippet;
		actions?: Snippet;
	} = $props();
</script>

<header class="header">
	<div class="heading">
		<h1 class="title">{title}</h1>
		{@render status?.()}
		<div class="actions">
			{@render actions?.()}
		</div>
	</div>
	{#if summary}
		<p class="summary">{summary}</p>
	{/if}
	<div class="meta">
		{@render meta?.()}
	</div>
</header>

<style>
	.header {
		padding: var(--space-8);
	}

	.heading {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}

	.title {
		margin: 0;
		font-size: var(--text-lg);
		line-height: var(--text-lg-leading);
		letter-spacing: var(--text-lg-tracking);
		font-weight: 600;
		color: var(--text-primary);
	}

	.summary {
		margin: var(--space-1) 0 0;
		font-size: var(--text-base);
		line-height: var(--text-base-leading);
		color: var(--text-secondary);
	}

	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-top: var(--space-4);
	}

	.actions {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin-left: auto;
	}
</style>
