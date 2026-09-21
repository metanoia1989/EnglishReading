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

// ECDICT packs several senses into one definition, one per line, and the extra
// lines carry their own part-of-speech or domain tag:
//
//   n.  狐狸, 狡猾的人
//       vi. 奸狡地行动, (书页)生斑, 变酸
//       [医] 表皮脱落
//
// Showing that as one block made the whole block selectable at once. Splitting
// it here means each row is exactly one sense — one part of speech, one line of
// meaning — which is also what gets stored on the annotation.
const LEADING_TAG = /^(?:([a-z]{1,6}\.)|(\[[^\]]{1,8}\]))\s*/i

function expandSense(sense) {
  const out = []
  String(sense.def ?? '')
    .split('\n')
    .forEach((raw, i) => {
      const line = raw.trim()
      if (!line) return
      let pos = ''
      let def = line
      const m = line.match(LEADING_TAG)
      if (m) {
        const rest = line.slice(m[0].length).trim()
        // A line that is nothing but a tag keeps the tag as its text.
        if (rest) {
          pos = (m[1] || m[2]).trim()
          def = rest
        }
      }
      // The first line's part of speech is the sense's own pos field.
      if (i === 0 && sense.pos) pos = sense.pos
      out.push({ pos, def })
    })
  return out
}

const groups = computed(() => {
  const seen = new Set()
  const out = []
  for (const sense of props.popup.senses || []) {
    for (const item of expandSense(sense)) {
      const key = item.pos + '\u0000' + item.def
      if (seen.has(key)) continue
      seen.add(key)
      out.push(item)
    }
  }
  return out
})

function isSelected(sense) {
  if (!props.existing) return false
  if (props.existing.pos !== (sense.pos || '')) return false
  if (props.existing.sense === sense.def) return true
  // Tolerate rows saved before the definitions were split per line.
  return String(props.existing.sense || '').split('\n')[0].trim() === sense.def
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
          <span class="pos">{{ sense.pos || '' }}</span>
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
  min-width: 40px;
  font-size: 12px;
  font-weight: 700;
  color: #6b7280;
}

.sense-row.selected .pos {
  color: var(--brand);
}

.def {
  flex: 1;
  min-width: 0;
  white-space: normal;
  overflow-wrap: anywhere;
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
