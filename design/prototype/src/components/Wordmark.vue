<script setup lang="ts">
defineProps<{ tone?: 'ink' | 'light' }>()
</script>

<template>
  <span class="wordmark" :class="`wordmark--${tone ?? 'ink'}`" aria-label="Nerae">NERAE</span>
</template>

<style scoped>
.wordmark {
  display: inline-block;
  font-family: var(--font-wordmark);
  font-size: var(--text-13);
  line-height: 1;
  font-weight: 600;
  letter-spacing: 0.28em;
  text-transform: uppercase;
  white-space: nowrap;
}

.wordmark--ink {
  color: var(--ink);
}

.wordmark--light {
  color: var(--focus-text);
}
</style>
