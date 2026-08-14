<script setup>
import { computed } from 'vue'

const props = defineProps({
  popup: { type: Object, required: true },
  existing: { type: Object, default: null },
})

const emit = defineEmits(['choose', 'close', 'remove'])

const style = computed(() => {
  if (props.popup.mobile) {
    return {
      left: `${props.popup.x}px`,
      top: 'auto',
      bottom: 'max(8px, env(safe-area-inset-bottom))',
    }
  }
  return {
    left: `${props.popup.x}px`,
    top: `${props.popup.y}px`,
  }
})

const groups = computed(() => {
  const seen = new Set()
  return (props.popup.senses || []).filter((s) => {
    const key = (s.pos || '') + '\u0000' + s.def
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
})

function isSelected(sense) {
  return (
    props.existing &&
    props.existing.pos === (sense.pos || '') &&
    props.existing.sense === sense.def
  )
}
</script>

<template>
  <Transition name="pop">
    <div
      v-if="popup.visible"
      class="dict-popup"
      :class="{ mobile: popup.mobile }"
      :style="style"
      @click.stop
    >
      <div class="pop-head">
        <div>
          <span class="word">{{ popup.word }}</span>
          <span v-if="popup.phonetic" class="phonetic">{{ popup.phonetic }}</span>
        </div>
        <button class="pop-close" title="关闭" @click="emit('close')">×</button>
      </div>

      <div v-if="popup.loading" class="pop-state"><div class="spinner" /></div>
      <div v-else-if="popup.error" class="pop-state">{{ popup.error }}</div>
      <div v-else-if="popup.notFound" class="pop-state">
        词典暂无收录：{{ popup.word }}
        <div class="hint">试试点击它的原形，或继续阅读积累生词</div>
      </div>

      <div v-else class="sense-list">
        <button
          v-for="(sense, i) in groups"
          :key="i"
          class="sense-row"
          :class="{ selected: isSelected(sense) }"
          @click="emit('choose', { pos: sense.pos || '', def: sense.def })"
        >
          <span v-if="sense.pos" class="pos">{{ sense.pos }}</span>
          <span class="def">{{ sense.def }}</span>
        </button>
      </div>

      <div v-if="existing" class="pop-foot">
        已选：<b>{{ existing.word }}</b>
        <span v-if="existing.pos"> {{ existing.pos }}</span> {{ existing.sense }}
        <button class="remove" @click="emit('remove', existing)">移除</button>
      </div>
      <div v-else-if="!popup.loading && !popup.notFound && !popup.error" class="pop-foot muted">
        点击一个词义，保存到原文单词下方
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.dict-popup {
  position: fixed;
  z-index: 90;
  width: 340px;
  max-width: calc(100vw - 24px);
  max-height: min(440px, calc(100vh - 24px));
  display: flex;
  flex-direction: column;
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 16px;
  box-shadow: 0 18px 48px rgba(24, 24, 27, 0.18);
  overflow: hidden;
}

.pop-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px 10px;
  border-bottom: 1px solid #f0efeb;
}

.word {
  font-family: var(--serif);
  font-size: 20px;
  font-weight: 700;
}

.phonetic {
  margin-left: 10px;
  color: var(--muted);
  font-size: 13px;
}

.pop-close {
  border: 0;
  background: transparent;
  font-size: 20px;
  color: var(--muted);
  padding: 0 4px;
}

.pop-state {
  padding: 26px 18px;
  color: var(--muted);
  font-size: 14px;
  line-height: 1.7;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

.hint {
  font-size: 12px;
  color: #a1a1aa;
}

.sense-list {
  overflow-y: auto;
  padding: 8px;
}

.sense-row {
  display: flex;
  align-items: baseline;
  gap: 8px;
  width: 100%;
  text-align: left;
  border: 0;
  background: transparent;
  border-radius: 9px;
  padding: 8px 10px;
  font-size: 13.5px;
  line-height: 1.6;
  color: var(--ink);
  transition: background 0.12s;
}

.sense-row:hover {
  background: #f4f4f5;
}

.sense-row.selected {
  background: #eef2ff;
  color: var(--brand);
}

.pos {
  flex-shrink: 0;
  min-width: 34px;
  font-size: 12px;
  font-weight: 700;
  color: #6b7280;
}

.sense-row.selected .pos {
  color: var(--brand);
}

.def {
  flex: 1;
  white-space: pre-line;
}

.pop-foot {
  padding: 10px 16px;
  border-top: 1px solid #f0efeb;
  background: #fafaf9;
  font-size: 12.5px;
  color: #52525b;
}

.pop-foot.muted {
  color: #a1a1aa;
}

.remove {
  margin-left: 8px;
  border: 0;
  background: transparent;
  color: #dc2626;
  font-size: 12.5px;
}

@media (max-width: 640px) {
  .dict-popup.mobile {
    width: calc(100vw - 12px);
    max-height: 46vh;
    border-radius: 16px 16px 6px 6px;
  }

  .dict-popup.mobile .pop-head {
    padding: 12px 14px 8px;
  }

  .dict-popup.mobile .word {
    font-size: 19px;
  }

  .dict-popup.mobile .sense-list {
    padding: 6px;
    -webkit-overflow-scrolling: touch;
  }

  .dict-popup.mobile .sense-row {
    min-height: 44px;
    padding: 9px 10px;
    font-size: 14px;
  }

  .dict-popup.mobile .pop-foot {
    padding: 9px 14px;
    padding-bottom: calc(9px + env(safe-area-inset-bottom));
  }
}

.pop-enter-active,
.pop-leave-active {
  transition: opacity 0.14s ease, transform 0.14s ease;
}

.pop-enter-from,
.pop-leave-to {
  opacity: 0;
  transform: translateY(4px);
}
</style>
