<script setup>
import { nextTick, ref, watch } from 'vue'

const props = defineProps({
  open: { type: Boolean, default: false },
  saving: { type: Boolean, default: false },
  placeholder: { type: String, default: '写下你的理解、语法点或疑问…' },
})

const emit = defineEmits(['save', 'cancel'])

const content = ref('')
const inputEl = ref(null)

watch(
  () => props.open,
  async (open) => {
    if (open) {
      await nextTick()
      inputEl.value?.focus()
      autoGrow()
    } else {
      content.value = ''
    }
  },
)

function save() {
  const text = content.value.trim()
  if (!text || props.saving) return
  emit('save', text)
}

// Grow/shrink by measuring, because "height: auto" cannot be animated.
function onEnter(el) {
  el.style.height = '0px'
  el.style.opacity = '0'
  void el.offsetHeight
  el.style.height = `${el.scrollHeight}px`
  el.style.opacity = '1'
}

function onAfterEnter(el) {
  el.style.height = 'auto'
}

function onLeave(el) {
  el.style.height = `${el.scrollHeight}px`
  void el.offsetHeight
  el.style.height = '0px'
  el.style.opacity = '0'
}

function autoGrow() {
  const el = inputEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 220)}px`
}
</script>

<template>
  <Transition name="grow" @enter="onEnter" @after-enter="onAfterEnter" @leave="onLeave">
    <div v-if="open" class="note-editor" @click.stop>
      <span class="note-tag">批</span>
      <div class="note-body">
        <textarea
          ref="inputEl"
          v-model="content"
          class="note-input"
          rows="2"
          :placeholder="placeholder"
          @input="autoGrow"
          @keydown.esc.prevent="emit('cancel')"
          @keydown.meta.enter.prevent="save"
          @keydown.ctrl.enter.prevent="save"
        />
        <div class="note-actions">
          <span class="note-hint">⌘/Ctrl + Enter 保存</span>
          <button class="note-btn" :disabled="saving" @click="emit('cancel')">取消</button>
          <button
            class="note-btn primary"
            :disabled="saving || !content.trim()"
            @click="save"
          >{{ saving ? '保存中…' : '保存批注' }}</button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
/* 展开与收起都从 0 高度动画到内容高度，所以高度由 JS 钩子显式赋值 */
.grow-enter-active,
.grow-leave-active {
  transition: height 0.3s cubic-bezier(0.22, 1, 0.36, 1), opacity 0.3s ease;
  overflow: hidden;
}

.note-editor {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin: 7px 0 0 4px;
  padding: 8px 10px;
  border-radius: 9px;
  border-left: 3px solid #fcd34d;
  background: #fffbeb;
}

.note-tag {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 700;
  border-radius: 5px;
  padding: 1px 6px;
  background: #fef3c7;
  color: #b45309;
}

.note-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.note-input {
  width: 100%;
  box-sizing: border-box;
  resize: none;
  overflow-y: auto;
  border: 1px solid #fde68a;
  border-radius: 7px;
  background: #fff;
  padding: 7px 9px;
  font-family: inherit;
  font-size: 13.5px;
  line-height: 1.65;
  color: #78350f;
  outline: none;
  transition: border-color 0.12s, box-shadow 0.12s;
}

.note-input::placeholder {
  color: #d6b370;
}

.note-input:focus {
  border-color: #fbbf24;
  box-shadow: 0 0 0 3px rgba(251, 191, 36, 0.18);
}

.note-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
}

.note-hint {
  margin-right: auto;
  font-size: 11px;
  color: #b45309;
  opacity: 0.7;
}

.note-btn {
  border: 1px solid #fcd34d;
  background: #fff;
  color: #92400e;
  border-radius: 7px;
  padding: 4px 10px;
  font-size: 12.5px;
  transition: background 0.12s;
}

.note-btn:hover:not(:disabled) {
  background: #fffbeb;
}

.note-btn.primary {
  background: #f59e0b;
  border-color: #f59e0b;
  color: #fff;
}

.note-btn.primary:hover:not(:disabled) {
  background: #d97706;
}

.note-btn:disabled {
  opacity: 0.5;
}

@media (max-width: 640px) {
  .note-editor {
    margin-left: 0;
  }

  .note-hint {
    display: none;
  }

  .note-input {
    font-size: 14px;
  }
}
</style>
