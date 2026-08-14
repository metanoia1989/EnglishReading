<script setup>
import { nextTick, ref, watch } from 'vue'

const props = defineProps({
  open: Boolean,
  title: String,
  saving: Boolean,
})

const emit = defineEmits(['close', 'save'])
const content = ref('')
const inputEl = ref(null)

watch(
  () => props.open,
  async (open) => {
    if (open) {
      await nextTick()
      inputEl.value?.focus()
    } else {
      content.value = ''
    }
  },
)

function save() {
  if (!content.value.trim()) return
  emit('save', content.value.trim())
  content.value = ''
}
</script>

<template>
  <Transition name="fade">
    <div v-if="open" class="note-mask" @click.self="emit('close')">
      <div class="note-modal">
        <h3>{{ title || '添加批注' }}</h3>
        <textarea
          ref="inputEl"
          v-model="content"
          class="note-textarea"
          placeholder="写下你的理解、语法点或疑问…"
        />
        <div class="note-actions">
          <button class="btn btn-ghost" @click="emit('close')">取消</button>
          <button class="btn btn-primary" :disabled="saving || !content.trim()" @click="save">
            {{ saving ? '保存中…' : '保存批注' }}
          </button>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.note-mask {
  position: fixed;
  inset: 0;
  z-index: 110;
  background: rgba(24, 24, 27, 0.4);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}

.note-modal {
  width: min(520px, 100%);
  background: #fff;
  border-radius: 18px;
  padding: 22px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.2);
}

.note-modal h3 {
  margin: 0 0 14px;
  font-size: 17px;
}

.note-textarea {
  width: 100%;
  min-height: 150px;
  resize: vertical;
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 12px;
  font-size: 14px;
  line-height: 1.7;
  outline: none;
}

.note-textarea:focus {
  border-color: var(--brand);
  box-shadow: 0 0 0 3px rgba(79, 70, 229, 0.12);
}

.note-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 14px;
}

@media (max-width: 640px) {
  .note-mask {
    align-items: flex-end;
    padding: 0;
  }

  .note-modal {
    width: 100%;
    border-radius: 18px 18px 0 0;
    padding: 20px 16px;
    padding-bottom: calc(20px + env(safe-area-inset-bottom, 0px));
  }

  .note-textarea {
    min-height: 130px;
    font-size: 16px;
  }

  .note-actions .btn {
    flex: 1;
    min-height: 42px;
  }
}
</style>
