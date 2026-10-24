<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { X } from '@lucide/vue'
import { isElementActuallyVisible } from '../utils/dom'

const props = defineProps<{
  open: boolean
  title: string
  labelledBy?: string
  returnFocus?: () => HTMLElement | null
}>()

const emit = defineEmits<{ close: [] }>()

const panel = ref<HTMLElement | null>(null)
let previousFocus: HTMLElement | null = null
let previousOverflow = ''

function close(): void {
  emit('close')
}


function focusableElements(): HTMLElement[] {
  if (!panel.value) return []
  return Array.from(
    panel.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [href], input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((element) => element.offsetParent !== null)
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key !== 'Tab' || !panel.value) return
  const focusables = focusableElements()
  if (!focusables.length) {
    event.preventDefault()
    panel.value.focus()
    return
  }
  const first = focusables[0]!
  const last = focusables[focusables.length - 1]!
  const active = document.activeElement
  const inside = active instanceof Node && panel.value.contains(active)
  if (!inside) {
    event.preventDefault()
    ;(event.shiftKey ? last : first).focus()
    return
  }
  if (event.shiftKey && (active === first || active === panel.value)) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    first.focus()
  }
}

watch(
  () => props.open,
  async (open) => {
    if (typeof document === 'undefined') return
    if (open) {
      previousFocus = document.activeElement as HTMLElement | null
      previousOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      document.addEventListener('keydown', onKeydown)
      await nextTick()
      const focusTarget = panel.value?.querySelector<HTMLElement>('[data-autofocus]')
      const firstFocusable = focusableElements()[0]
      ;(focusTarget ?? firstFocusable ?? panel.value)?.focus()
    } else {
      document.body.style.overflow = previousOverflow
      document.removeEventListener('keydown', onKeydown)
      if (isElementActuallyVisible(previousFocus)) {
        previousFocus.focus()
        return
      }
      const fallback = props.returnFocus?.() ?? null
      if (isElementActuallyVisible(fallback)) {
        fallback.focus()
        return
      }
      document.getElementById('main')?.focus()
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  if (typeof document === 'undefined') return
  document.body.style.overflow = previousOverflow
  document.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="scrim">
      <div v-if="open" class="scrim" @click="close" />
    </Transition>
    <Transition name="sheet">
      <section
        v-if="open"
        ref="panel"
        class="sheet"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        tabindex="-1"
      >
        <header class="sheet-head">
          <h2 class="sheet-title">{{ title }}</h2>
          <button class="icon-btn sheet-close" type="button" aria-label="关闭" @click="close">
            <X :size="20" aria-hidden="true" />
          </button>
        </header>
        <div class="sheet-body">
          <slot />
        </div>
        <footer v-if="$slots.footer" class="sheet-foot">
          <slot name="footer" />
        </footer>
      </section>
    </Transition>
  </Teleport>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  z-index: 100;
  background: var(--scrim);
}

.sheet {
  position: fixed;
  z-index: 101;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  flex-direction: column;
  max-height: min(86dvh, 760px);
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  background: var(--surface);
  box-shadow: var(--shadow-sheet);
  outline: none;
}

.sheet-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 12px 8px var(--sp-5);
  border-bottom: 1px solid var(--line);
}

.sheet-title {
  font-size: var(--text-17);
  line-height: 1.4;
  font-weight: 600;
}

.sheet-close {
  margin-left: auto;
}

.sheet-body {
  overflow-y: auto;
  overscroll-behavior: contain;
  padding: var(--sp-5);
  -webkit-overflow-scrolling: touch;
}

.sheet-foot {
  padding: var(--sp-3) var(--sp-5) calc(var(--sp-4) + env(safe-area-inset-bottom));
  border-top: 1px solid var(--line);
  background: var(--surface);
}

@media (min-width: 640px) {
  .sheet {
    left: 0;
    right: 0;
    top: 0;
    bottom: 0;
    margin: auto;
    width: min(560px, calc(100vw - 48px));
    height: fit-content;
    max-height: min(80dvh, 760px);
    border-radius: var(--radius-lg);
    box-shadow: var(--shadow-dialog);
  }

  .sheet-head {
    padding: 16px 16px 12px var(--sp-6);
  }

  .sheet-body {
    padding: var(--sp-5) var(--sp-6);
  }

  .sheet-foot {
    padding: var(--sp-4) var(--sp-6);
  }
}
</style>
