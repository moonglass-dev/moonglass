<script lang="ts">
	import '$lib/tokens.css';
	import '$lib/fonts.css';

	import type { Snippet } from 'svelte';

	let { children }: { children: Snippet } = $props();
</script>

{@render children()}
