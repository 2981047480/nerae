<script setup lang="ts">
import { ref } from 'vue'
import { ImageOff } from '@lucide/vue'

const props = withDefaults(
  defineProps<{
    src: string
    alt: string
    eager?: boolean
    fallbackLabel?: string
  }>(),
  { eager: false, fallbackLabel: '图片暂不可用' },
)

const failed = ref(false)
</script>

<template>
  <div class="photo" :class="{ 'photo--failed': failed }">
    <img
      v-if="!failed"
      :src="src"
      :alt="alt"
      :loading="props.eager ? 'eager' : 'lazy'"
      decoding="async"
      @error="failed = true"
    />
    <div v-else class="photo-fallback" role="img" :aria-label="`${alt}（${fallbackLabel}）`">
      <ImageOff :size="22" aria-hidden="true" />
      <span>{{ fallbackLabel }}</span>
    </div>
  </div>
</template>

<style scoped>
.photo {
  width: 100%;
  height: 100%;
  background: #dfe1dc;
  overflow: hidden;
}

.photo img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.photo--failed {
  background:
    linear-gradient(135deg, rgba(25, 47, 57, 0.06) 25%, transparent 25%) -8px 0/16px 16px,
    linear-gradient(225deg, rgba(25, 47, 57, 0.06) 25%, transparent 25%) -8px 0/16px 16px,
    #e8e8e3;
}

.photo-fallback {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  height: 100%;
  color: var(--muted);
  font-size: var(--text-13);
  line-height: 1.5;
  text-align: center;
  padding: 16px;
}
</style>
