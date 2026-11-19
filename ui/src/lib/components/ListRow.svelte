<script lang="ts">
	import type { Snippet } from 'svelte';

	let { id, highlighted, onopen, children }: {
		id: string;
		highlighted: boolean;
		onopen: () => void;
		children: Snippet;
	} = $props();
</script>

<li class="row" class:highlighted>
	<button type="button" class="row-button" id={id} onclick={onopen}>
		{@render children()}
	</button>
</li>

<style>
	.row {
		height: 36px;
		box-sizing: border-box;
		border-radius: 0;
		border-bottom: 1px solid var(--border-subtle);
		transition:
			background-color var(--duration-micro) var(--ease-out),
			border-color var(--duration-micro) var(--ease-out);
	}

	.row:hover {
		background: var(--surface-hover);
	}

	.row.highlighted {
		background: var(--surface-active);
	}

	.row:hover,
	.row.highlighted,
	.row:has(+ .row:hover),
	.row:has(+ .row.highlighted) {
		border-bottom-color: transparent;
	}

	.row-button {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		width: 100%;
		height: 100%;
		margin: 0;
		padding: 0 var(--space-4);
		border: 0;
		background: none;
		color: inherit;
		font: inherit;
		font-size: var(--text-base);
		line-height: var(--text-base-leading);
		text-align: left;
		cursor: pointer;
	}

	.row-button:focus-visible {
		outline: var(--focus-ring-width) solid var(--accent);
		outline-offset: calc(-1 * (var(--focus-ring-width) + var(--focus-ring-offset)));
	}
</style>
