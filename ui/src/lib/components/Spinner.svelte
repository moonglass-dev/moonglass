<script lang="ts">
	let { kind }: { kind: 'building' | 'switching' } = $props();
</script>

<svg class="spinner" class:switching={kind === 'switching'} viewBox="0 0 14 14" width="14" height="14" aria-hidden="true" focusable="false">
	<circle cx="7" cy="7" r="5.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-dasharray="22 13" />
</svg>

<style>
	.spinner {
		color: var(--state-info);
		animation: spin 800ms linear infinite;
	}

	.switching {
		color: var(--state-warn);
	}

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.spinner {
			animation: none;
		}
	}
</style>
