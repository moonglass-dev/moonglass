<script lang="ts">
	const uid = $props.id();
</script>

<svg viewBox="0 0 22 22" width="22" height="22" aria-hidden="true" focusable="false" class="orb">
	<defs>
		<radialGradient id="{uid}-glass" cx="0.35" cy="0.3" r="0.85">
			<stop offset="0" stop-color="#3d4655" />
			<stop offset="0.55" stop-color="#1b2029" />
			<stop offset="1" stop-color="#0d1016" />
		</radialGradient>
		<linearGradient id="{uid}-rim" x1="0" y1="0" x2="0" y2="1">
			<stop offset="0" stop-color="#ffffff" stop-opacity="0.7" />
			<stop offset="1" stop-color="#ffffff" stop-opacity="0.08" />
		</linearGradient>
		<clipPath id="{uid}-clip">
			<circle cx="11" cy="11" r="10" />
		</clipPath>
	</defs>
	<circle cx="11" cy="11" r="10" fill="url(#{uid}-glass)" />
	<g clip-path="url(#{uid}-clip)" fill="none" stroke-width="1">
		<path d="M-2 13.5C4 11.5 9 12 14 9.5S20 6 24 5" stroke="#2fb5b5" stroke-opacity="0.5" />
		<path d="M-1 16.5C5 14.5 10 15 15 12.5S21 9 24 8" stroke="#5a8fd8" stroke-opacity="0.45" />
	</g>
	<circle cx="11" cy="11" r="10.5" fill="none" stroke="url(#{uid}-rim)" stroke-width="1" />
</svg>
