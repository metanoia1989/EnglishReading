<script setup>
import { computed } from 'vue'

const props = defineProps({
  sentence: { type: Object, required: true },
  wordAnnotations: { type: Array, default: () => [] },
  translation: { type: Object, default: null },
  notes: { type: Array, default: () => [] },
  busy: { type: Boolean, default: false },
  showActions: { type: Boolean, default: true },
})

const emit = defineEmits([
  'word-click',
  'translate',
  'annotate',
  'delete-word',
  'delete-translation',
  'delete-note',
])

const segments = computed(() => tokenize(props.sentence.text))
const picks = computed(() =>
  props.wordAnnotations
    .filter((a) => a.sentenceIndex === props.sentence.index)
    .slice()
    .sort((a, b) => a.wordIndex - b.wordIndex),
)

function tokenize(text) {
  const out = []
  const re = /[A-Za-z]+(?:['’](?:s|t|d|m|re|ve|ll))?/gi
  let last = 0
  let index = 0
  let match
  while ((match = re.exec(text))) {
    if (match.index > last) out.push({ text: text.slice(last, match.index), isWord: false })
    out.push({
      text: match[0],
      isWord: true,
      wordIndex: index,
      before: text.slice(Math.max(0, match.index - 1), match.index),
    })
    index += 1
    last = match.index + match[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last), isWord: false })
  return out
}

function hasPick(wordIndex) {
  return picks.value.some((a) => a.wordIndex === wordIndex)
}

function pickFor(wordIndex) {
  return picks.value.find((a) => a.wordIndex === wordIndex)
}

function pickLabel(a) {
  return [a.pos, a.sense].filter(Boolean).join(' ')
}

function onWordClick(seg, event) {
  emit('word-click', { ...seg, sentence: props.sentence }, event)
}
</script>

<template>
  <div class="sentence-item">
    <div class="sentence-row">
      <p class="sentence-text">
        <template v-for="(seg, i) in segments" :key="i">
          <span
            v-if="seg.isWord"
            class="word-token"
            :class="{ picked: hasPick(seg.wordIndex) }"
            :title="hasPick(seg.wordIndex) ? pickFor(seg.wordIndex).sense : '点击查词'"
            @click.stop="onWordClick(seg, $event)"
          >{{ seg.text }}</span>
          <template v-else>{{ seg.text }}</template>
        </template>
      </p>

      <div v-if="showActions" class="sentence-actions">
        <button
          class="mini-btn"
          :class="{ active: translation }"
          :disabled="busy"
          title="翻译本句"
          @click="emit('translate', sentence)"
        >译</button>
        <button class="mini-btn" title="添加句子批注" @click="emit('annotate', sentence)">批</button>
      </div>
    </div>

    <!-- 第 2 行：选中的词性 + 词义，颜色更浅 -->
    <div v-if="picks.length" class="line word-picks">
      <span class="line-tag">词</span>
      <span v-for="a in picks" :key="a.id" class="pick-chip">
        <b>{{ a.word }}</b>
        <span v-if="a.pos"> {{ a.pos }}</span>
        <span class="pick-sense">{{ a.sense }}</span>
        <button class="chip-close" title="取消这个单词的标注" @click="emit('delete-word', a)">×</button>
      </span>
    </div>

    <!-- 第 3 行：翻译 -->
    <div v-if="translation" class="line translation-line">
      <span class="line-tag">译</span>
      <span class="line-content">{{ translation.translatedText }}</span>
      <button class="chip-close" title="删除翻译" @click="emit('delete-translation', translation)">×</button>
    </div>

    <!-- 第 4 行：批注 -->
    <div v-for="note in notes" :key="note.id" class="line note-line">
      <span class="line-tag">批</span>
      <span class="line-content">{{ note.content }}</span>
      <button class="chip-close" title="删除批注" @click="emit('delete-note', note)">×</button>
    </div>
  </div>
</template>

<style scoped>
.sentence-item {
  padding: 10px 0;
  border-bottom: 1px dashed rgba(231, 229, 224, 0.7);
}

.sentence-item:last-child {
  border-bottom: 0;
}

.sentence-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
}

.sentence-text {
  flex: 1;
  margin: 0;
  font-family: var(--serif);
  font-size: 18.5px;
  line-height: 1.9;
  letter-spacing: 0.01em;
}

.word-token {
  display: inline-block;
  cursor: pointer;
  border-radius: 5px;
  padding: 1px 2px;
  margin: 0 -1px;
  transition: background 0.12s, color 0.12s;
}

.word-token:hover {
  background: #fef08a;
}

.word-token.picked {
  background: rgba(139, 92, 246, 0.12);
  border-bottom: 1.5px solid rgba(139, 92, 246, 0.55);
  color: #6d28d9;
}

.sentence-actions {
  display: flex;
  gap: 6px;
  opacity: 0;
  transition: opacity 0.15s;
  padding-top: 3px;
}

.sentence-item:hover .sentence-actions {
  opacity: 1;
}

.mini-btn {
  width: 26px;
  height: 26px;
  border: 1px solid var(--line);
  background: #fff;
  border-radius: 8px;
  font-size: 12px;
  color: #71717a;
  line-height: 1;
}

.mini-btn:hover,
.mini-btn.active {
  border-color: #a5b4fc;
  color: var(--brand);
  background: var(--brand-soft);
}

.mini-btn:disabled {
  opacity: 0.5;
}

.line {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin: 7px 0 0 4px;
  padding: 6px 10px;
  border-radius: 9px;
  font-size: 13.5px;
  line-height: 1.7;
  position: relative;
}

.line-tag {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 700;
  border-radius: 5px;
  padding: 1px 6px;
}

.line-content {
  flex: 1;
  white-space: pre-wrap;
}

.word-picks {
  background: rgba(139, 92, 246, 0.045);
  color: #9d8ac4;
}

.word-picks .line-tag {
  background: #ede9fe;
  color: #7c3aed;
}

.pick-chip {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
  margin-right: 10px;
  color: #9d8ac4;
}

.pick-chip b {
  color: #8b5cf6;
}

.pick-sense {
  color: #a79ac9;
}

.translation-line {
  background: #f0f9ff;
  color: #075985;
  border-left: 3px solid #7dd3fc;
}

.translation-line .line-tag {
  background: #e0f2fe;
  color: #0369a1;
}

.note-line {
  background: #fffbeb;
  color: #92400e;
  border-left: 3px solid #fcd34d;
}

.note-line .line-tag {
  background: #fef3c7;
  color: #b45309;
}

.chip-close {
  border: 0;
  background: transparent;
  color: inherit;
  opacity: 0.55;
  font-size: 15px;
  padding: 0 2px;
  flex-shrink: 0;
}

.chip-close:hover {
  opacity: 1;
}

@media (hover: none) {
  .sentence-actions {
    opacity: 1;
  }
}

@media (max-width: 640px) {
  .sentence-item {
    padding: 9px 0;
  }

  .sentence-text {
    font-size: 17.5px;
    line-height: 1.85;
  }

  .word-token {
    padding: 3px 3px;
    margin: 0 -2px;
    border-radius: 6px;
  }

  .sentence-actions {
    opacity: 1;
    gap: 5px;
  }

  .mini-btn {
    width: 30px;
    height: 30px;
    font-size: 12.5px;
    border-radius: 9px;
  }

  .line {
    margin: 6px 0 0 1px;
    padding: 6px 9px;
    font-size: 13px;
  }

  .pick-chip {
    margin-right: 6px;
  }
}
</style>
