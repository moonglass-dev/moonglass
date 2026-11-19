<script lang="ts" generics="T">
	import type { Snippet } from 'svelte';

	import ListRow from './ListRow.svelte';

	let {
		label,
		items,
		key,
		row,
		onopen,
		highlighted = $bindable(0)
	}: {
		label: string;
		items: T[];
		key: (item: T) => string;
		row: Snippet<[T]>;
		onopen: (item: T) => void;
		highlighted?: number;
	} = $props();

	const uid = $props.id();

	function isEditable(target: EventTarget | null): boolean {
		return (
			target instanceof HTMLElement &&
			(target.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName))
		);
	}

	// A focused link or button already acts on Enter, so the list must not open
	// the highlighted row as well (docs/specs/ui.md §4: the list owns the key).
	function isControl(target: EventTarget | null): boolean {
		return (
			target instanceof HTMLElement &&
			target.closest('a[href], button, [role="button"], summary') !== null
		);
	}

	function focus(highlighted: number): void {
		document.getElementById(`${uid}-${highlighted}`)?.focus();
	}

	function handleKey(event: KeyboardEvent): void {
		if (event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented) {
			return;
		}
		if (items.length === 0) {
			return;
		}
		if (isEditable(event.target)) {
			return;
		}

		switch (event.key) {
			case 'j':
			case 'ArrowDown':
				highlighted = Math.min(items.length - 1, highlighted + 1);
				event.preventDefault();
				focus(highlighted);
				break;
			case 'k':
			case 'ArrowUp':
				highlighted = Math.max(0, highlighted - 1);
				event.preventDefault();
				focus(highlighted);
				break;
			case 'Enter':
				if (isControl(event.target)) {
					return;
				}
				onopen(items[Math.min(highlighted, items.length - 1)]);
				break;
		}
	}
</script>

<svelte:window onkeydown={handleKey} />

<ul class="list" aria-label={label}>
	{#each items as item, i (key(item))}
		<ListRow
			id="{uid}-{i}"
			highlighted={i === highlighted}
			onopen={() => {
				highlighted = i;
				onopen(item);
			}}
		>
			{@render row(item)}
		</ListRow>
	{/each}
</ul>

<style>
	.list {
		margin: 0;
		padding: 0;
		list-style: none;
		width: 100%;
	}
</style>
