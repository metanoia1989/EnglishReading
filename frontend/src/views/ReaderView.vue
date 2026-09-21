<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { contentApi } from '../api'
import { auth } from '../store/auth'
import SentenceItem from '../components/SentenceItem.vue'
import WordPopup from '../components/WordPopup.vue'
import InlineNoteEditor from '../components/InlineNoteEditor.vue'

const route = useRoute()
const router = useRouter()

const article = ref(null)
const dataset = ref(null)
const paragraphs = ref([])
// The whole article payload, kept for the stale-anchor count it carries.
const articleDetail = ref(null)
const leftArticles = ref([])
const loading = ref(true)
const loadError = ref('')

const wordAnnotations = ref([])
const notes = ref([])
const translations = ref([])

const leftOpen = ref(window.innerWidth >= 1000)
const tocOpen = ref(window.innerWidth >= 1200)
const mobileOpen = ref('') // 'left' | 'toc' | ''

const busy = reactive(new Set())
const toast = ref('')
let toastTimer = null

const popup = reactive({
  visible: false,
  x: 0,
  y: 0,
  mobile: false,
  word: '',
  phonetic: '',
  senses: [],
  loading: false,
  notFound: false,
  error: '',
  context: null,
})

const noteEditor = reactive({
  open: false,
  title: '',
  paragraphHash: null,
  sentenceIndex: -1,
  saving: false,
})

const mainEl = ref(null)
const activeAnchor = ref('')
let observer = null

// publishedLabel renders the article's date when the content file carries one.
const publishedLabel = computed(() => {
  const raw = article.value?.publishedAt
  if (!raw) return ''
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return ''
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
})

// ---------- data loading ----------

async function loadArticle(id) {
  loading.value = true
  loadError.value = ''
  try {
    const data = await contentApi.article(id)
    article.value = data.article
    dataset.value = data.dataset
    paragraphs.value = data.paragraphs
    articleDetail.value = data
    document.title = `${data.article.title} · 拾句`

    const tasks = []
    if (auth.isLoggedIn) tasks.push(loadState(id))
    if (!leftArticles.value.length || leftArticles.value[0]?.datasetId !== data.article.datasetId) {
      tasks.push(loadLeftArticles(data.article.datasetId))
    }
    if (tasks.length) await Promise.all(tasks)

    mainEl.value?.scrollTo({ top: 0 })
    activeAnchor.value = ''
    await nextTick()
    setupObserver()
  } catch (e) {
    loadError.value = e.message
  } finally {
    loading.value = false
  }
}

async function loadLeftArticles(datasetId) {
  try {
    const data = await contentApi.datasetArticles(datasetId)
    leftArticles.value = data.articles
  } catch {
    leftArticles.value = []
  }
}

async function loadState(id) {
  try {
    const data = await contentApi.state(id)
    wordAnnotations.value = data.wordAnnotations
    notes.value = data.notes
    translations.value = data.translations
  } catch {
    // Token may be stale; the api layer clears it.
  }
}

onMounted(() => {
  loadArticle(route.params.id)
  document.addEventListener('click', onDocumentClick)
  window.addEventListener('resize', handleViewportResize)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentClick)
  window.removeEventListener('resize', handleViewportResize)
  observer?.disconnect()
})

function handleViewportResize() {
  closePopup()
  // 跨断点时收敛侧栏状态，避免桌面开启的抽屉在手机上突然遮住正文。
  if (window.innerWidth < 900) {
    leftOpen.value = false
    tocOpen.value = false
    mobileOpen.value = ''
  } else {
    mobileOpen.value = ''
  }
}

watch(
  () => route.params.id,
  (id) => {
    if (id) loadArticle(id)
  },
)

function showToast(message) {
  toast.value = message
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (toast.value = ''), 2200)
}

// ---------- left list / TOC ----------

const toc = computed(() =>
  paragraphs.value.flatMap((p) => {
    if (p.kind === 'heading') {
      return [{ id: `p-${p.hash}`, label: p.content, heading: true }]
    }
    const preview = p.content.length > 22 ? `${p.content.slice(0, 22)}…` : p.content
    return [{ id: `p-${p.hash}`, label: `¶ ${p.index}  ${preview}`, heading: false }]
  }),
)

function scrollToAnchor(id) {
  const el = document.getElementById(id)
  if (!el) return
  activeAnchor.value = id
  const top = el.getBoundingClientRect().top - mainEl.value.getBoundingClientRect().top + mainEl.value.scrollTop - 14
  mainEl.value?.scrollTo({ top, behavior: 'smooth' })
  mobileOpen.value = ''
}

function setupObserver() {
  observer?.disconnect()
  const root = mainEl.value
  if (!root) return
  observer = new IntersectionObserver(
    (entries) => {
      const visible = entries
        .filter((e) => e.isIntersecting)
        .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
      if (visible[0]) activeAnchor.value = visible[0].target.id
    },
    { root, rootMargin: '-20% 0px -70% 0px', threshold: 0 },
  )
  document.querySelectorAll('[data-anchor]').forEach((el) => observer.observe(el))
}

function toggleLeft() {
  if (window.innerWidth < 900) {
    mobileOpen.value = mobileOpen.value === 'left' ? '' : 'left'
  } else {
    leftOpen.value = !leftOpen.value
  }
}

function toggleToc() {
  if (window.innerWidth < 1200) {
    mobileOpen.value = mobileOpen.value === 'toc' ? '' : 'toc'
  } else {
    tocOpen.value = !tocOpen.value
  }
}

// ---------- dictionary popup ----------

function normalizeWord(raw) {
  let w = raw.toLowerCase().replace(/^[^a-z]+|[^a-z]+$/g, '')
  w = w.replace(/['’]s$/, '')
  return w
}

function onWordClick(paragraph, token, event) {
  popup.context = {
    paragraphHash: paragraph.hash,
    sentenceIndex: token.sentence.index,
    wordIndex: token.wordIndex,
    word: token.text,
  }
  popup.word = normalizeWord(token.text)
  popup.phonetic = ''
  popup.senses = []
  popup.loading = true
  popup.notFound = false
  popup.error = ''
  popup.visible = true

  const small = window.innerWidth < 640
  popup.mobile = small
  if (small) {
    // 手机端做成底部词典抽屉，拇指操作更顺手。
    popup.x = 6
    const height = Math.min(300, window.innerHeight * 0.46)
    popup.y = Math.max(6, window.innerHeight - height - 8)
  } else {
    const width = 350
    const height = 430
    popup.x = Math.max(8, Math.min(event.clientX, window.innerWidth - width - 8))
    popup.y = Math.max(8, Math.min(event.clientY + 10, window.innerHeight - height - 8))
  }

  contentApi
    .lookup(popup.word)
    .then((data) => {
      if (popup.context?.paragraphHash !== paragraph.hash || popup.context?.wordIndex !== token.wordIndex) return
      popup.word = data.word
      popup.phonetic = data.phonetic || ''
      popup.senses = data.senses || []
      popup.notFound = !data.found
    })
    .catch((e) => {
      popup.error = e.message
    })
    .finally(() => {
      popup.loading = false
    })
}

function closePopup() {
  popup.visible = false
  popup.context = null
}

function onDocumentClick(event) {
  if (popup.visible && !event.target.closest('.dict-popup') && !event.target.closest('.word-token')) {
    closePopup()
  }
}

const popupExisting = computed(() => {
  if (!popup.context) return null
  return (
    wordAnnotations.value.find(
      (a) =>
        a.paragraphHash === popup.context.paragraphHash &&
        a.sentenceIndex === popup.context.sentenceIndex &&
        a.wordIndex === popup.context.wordIndex,
    ) || null
  )
})

function requireLogin() {
  if (auth.isLoggedIn) return true
  router.push({ path: '/login', query: { redirect: route.fullPath } })
  return false
}

async function chooseSense(sense) {
  if (!popup.context || !requireLogin()) return
  try {
    const saved = await contentApi.saveWordAnnotation(route.params.id, {
      paragraph_hash: popup.context.paragraphHash,
      sentence_index: popup.context.sentenceIndex,
      word_index: popup.context.wordIndex,
      word: popup.context.word,
      pos: sense.pos,
      sense: sense.def,
    })
    const sameKey = (a) =>
      a.paragraphHash === saved.paragraphHash &&
      a.sentenceIndex === saved.sentenceIndex &&
      a.wordIndex === saved.wordIndex
    wordAnnotations.value = [...wordAnnotations.value.filter((a) => !sameKey(a)), saved]
    showToast(`已标注 ${saved.word} · ${saved.sense}`)
  } catch (e) {
    showToast(e.message)
  }
}

async function removeWordAnnotation(annotation) {
  if (!auth.isLoggedIn) return
  try {
    await contentApi.deleteWordAnnotation(route.params.id, annotation.id)
    wordAnnotations.value = wordAnnotations.value.filter((a) => a.id !== annotation.id)
    showToast('已移除单词标注')
  } catch (e) {
    showToast(e.message)
  }
}

// ---------- translations ----------

function translationFor(paragraphHash, sentenceIndex) {
  return (
    translations.value.find(
      (t) => t.paragraphHash === paragraphHash && t.sentenceIndex === sentenceIndex,
    ) || null
  )
}

function isBusy(key) {
  return busy.has(key)
}

async function translate(paragraph, sentenceIndex) {
  if (!requireLogin()) return
  const key = `${paragraph.hash}:${sentenceIndex}`
  if (busy.has(key)) return
  busy.add(key)
  try {
    const saved = await contentApi.translate(route.params.id, {
      paragraph_hash: paragraph.hash,
      sentence_index: sentenceIndex,
    })
    translations.value = [
      ...translations.value.filter(
        (t) => !(t.paragraphHash === saved.paragraphHash && t.sentenceIndex === saved.sentenceIndex),
      ),
      saved,
    ]
  } catch (e) {
    showToast(e.message)
  } finally {
    busy.delete(key)
  }
}

function translateSentence(paragraph, sentence) {
  translate(paragraph, sentence.index)
}

function translateParagraph(paragraph) {
  translate(paragraph, -1)
}

async function removeTranslation(translation) {
  if (!auth.isLoggedIn) return
  try {
    await contentApi.deleteTranslation(route.params.id, translation.id)
    translations.value = translations.value.filter((t) => t.id !== translation.id)
  } catch (e) {
    showToast(e.message)
  }
}

// ---------- notes ----------

// 批注不再是弹窗：在句子/段落内部就地展开一个输入表单，提交或取消后收起。
// 同一个位置再次点击「批」则收起（切换）。
function openNote(paragraph, sentence = null) {
  if (!requireLogin()) return
  const sentenceIndex = sentence ? sentence.index : -1
  const same =
    noteEditor.open &&
    noteEditor.paragraphHash === paragraph.hash &&
    noteEditor.sentenceIndex === sentenceIndex
  if (same) {
    closeNote()
    return
  }
  noteEditor.paragraphHash = paragraph.hash
  noteEditor.sentenceIndex = sentenceIndex
  noteEditor.title = sentence
    ? `句子批注 · ${paragraph.index}-${sentence.index + 1}`
    : `段落批注 · 第 ${paragraph.index} 段`
  noteEditor.open = true
}

function closeNote() {
  noteEditor.open = false
  noteEditor.paragraphHash = null
  noteEditor.sentenceIndex = -1
}

function noteOpenFor(paragraphHash, sentenceIndex) {
  return (
    noteEditor.open &&
    noteEditor.paragraphHash === paragraphHash &&
    noteEditor.sentenceIndex === sentenceIndex
  )
}

const paragraphNoteOpen = (paragraphHash) => noteOpenFor(paragraphHash, -1)

async function saveNote(content) {
  noteEditor.saving = true
  try {
    const saved = await contentApi.createNote(route.params.id, {
      paragraph_hash: noteEditor.paragraphHash,
      sentence_index: noteEditor.sentenceIndex,
      content,
    })
    notes.value = [...notes.value, saved]
    closeNote()
    showToast('批注已保存')
  } catch (e) {
    showToast(e.message)
  } finally {
    noteEditor.saving = false
  }
}

async function removeNote(note) {
  if (!auth.isLoggedIn) return
  try {
    await contentApi.deleteNote(route.params.id, note.id)
    notes.value = notes.value.filter((n) => n.id !== note.id)
  } catch (e) {
    showToast(e.message)
  }
}

function notesFor(paragraphHash, sentenceIndex) {
  return notes.value.filter((n) => n.paragraphHash === paragraphHash && n.sentenceIndex === sentenceIndex)
}

function wordAnnFor(paragraphHash, sentenceIndex) {
  return wordAnnotations.value.filter(
    (a) => a.paragraphHash === paragraphHash && a.sentenceIndex === sentenceIndex,
  )
}

function goArticle(id) {
  if (!id) return
  router.push(`/read/${id}`)
  mobileOpen.value = ''
}

function isCurrentArticle(id) {
  return String(id) === String(route.params.id)
}

const currentIndex = computed(() => {
  if (!article.value || !leftArticles.value.length) return -1
  return leftArticles.value.findIndex((a) => a.id === article.value.id)
})

const prevArticle = computed(() => (currentIndex.value > 0 ? leftArticles.value[currentIndex.value - 1] : null))
const nextArticle = computed(() =>
  currentIndex.value >= 0 && currentIndex.value < leftArticles.value.length - 1
    ? leftArticles.value[currentIndex.value + 1]
    : null,
)
</script>

<template>
  <div class="reader-shell">
    <!-- 顶部工具条 -->
    <header class="reader-topbar">
      <div class="tb-left">
        <button class="icon-btn" title="文章列表" @click="toggleLeft">☰</button>
        <RouterLink class="tb-back" :to="article ? `/dataset/${article.datasetId}` : '/'">←</RouterLink>
        <div class="tb-title">
          <span v-if="article">{{ article.title }}</span>
          <small v-if="dataset">{{ dataset.emoji }} {{ dataset.title }}</small>
        </div>
      </div>

      <div class="tb-right">
        <button class="icon-btn" :disabled="!prevArticle" title="上一篇" @click="goArticle(prevArticle?.id)">↑</button>
        <button class="icon-btn" :disabled="!nextArticle" title="下一篇" @click="goArticle(nextArticle?.id)">↓</button>
        <button class="icon-btn toc-toggle" title="目录" @click="toggleToc">☷</button>
        <template v-if="auth.isLoggedIn">
          <span class="tb-user">{{ auth.user.nickname || auth.user.email }}</span>
          <button class="tb-link" @click="router.push('/')">书库</button>
        </template>
        <template v-else>
          <button class="tb-login" @click="router.push({ path: '/login', query: { redirect: route.fullPath } })">登录</button>
        </template>
      </div>
    </header>

    <div class="reader-body">
      <!-- 左侧：可折叠固定文章列表 -->
      <aside class="left-pane" :class="{ open: leftOpen || mobileOpen === 'left' }">
        <div class="pane-head">
          <div>
            <strong>{{ dataset?.title || '文章列表' }}</strong>
            <small>{{ leftArticles.length }} 篇</small>
          </div>
          <button class="icon-btn" @click="toggleLeft">×</button>
        </div>
        <div class="article-list">
          <button
            v-for="a in leftArticles"
            :key="a.id"
            class="list-item"
            :class="{ active: isCurrentArticle(a.id) }"
            @click="goArticle(a.id)"
          >
            <span class="list-index">{{ String(a.id).padStart(2, '0') }}</span>
            <span class="list-title">{{ a.title }}</span>
          </button>
        </div>
      </aside>

      <!-- 中间：正文阅读 -->
      <main ref="mainEl" class="reading-main">
        <div v-if="loading" class="center-wrap"><div class="spinner" /></div>
        <p v-else-if="loadError" class="error-text" style="padding: 40px">{{ loadError }}</p>

        <article v-else class="article">
          <header class="article-head">
            <h1>{{ article.title }}</h1>
            <p v-if="article.subtitle" class="subtitle">{{ article.subtitle }}</p>
            <p class="meta">
              <span v-if="article.level" class="level-tag">{{ article.level }}</span>
              <span v-if="article.author" class="author">{{ article.author }}</span>
              <span v-if="publishedLabel" class="published">{{ publishedLabel }}</span>
              <span>{{ paragraphs.filter((p) => p.kind === 'text').length }} 段 · 点击单词查词，悬停句子可翻译或批注</span>
            </p>
          </header>

          <!-- Anchors whose paragraph text changed are reported, never deleted. -->
          <div v-if="articleDetail && articleDetail.staleAnchors > 0" class="stale-notice">
            原文有段落被修改过：<b>{{ articleDetail.staleAnchors }}</b> 处标注已无法定位到原文（未删除）。
            如需清理，请在对应位置重新标注或移除。
          </div>

          <section v-for="p in paragraphs" :key="p.hash">
            <h2 v-if="p.kind === 'heading'" :id="`p-${p.hash}`" class="para-heading" data-anchor>
              {{ p.content }}
            </h2>

            <div v-else :id="`p-${p.hash}`" class="paragraph-card" data-anchor>
              <div class="para-meta">第 {{ p.index }} 段 · {{ p.sentences.length }} 句</div>

              <SentenceItem
                v-for="s in p.sentences"
                :key="`${p.hash}-${s.index}`"
                :sentence="s"
                :word-annotations="wordAnnFor(p.hash, s.index)"
                :translation="translationFor(p.hash, s.index)"
                :notes="notesFor(p.hash, s.index)"
                :busy="isBusy(`${p.hash}:${s.index}`)"
                :note-open="noteOpenFor(p.hash, s.index)"
                :note-saving="noteEditor.saving"
                @word-click="(token, ev) => onWordClick(p, token, ev)"
                @translate="translateSentence(p, s)"
                @annotate="openNote(p, s)"
                @delete-word="removeWordAnnotation"
                @delete-translation="removeTranslation"
                @delete-note="removeNote"
                @note-save="saveNote"
                @note-cancel="closeNote"
              />

              <!-- 段落级翻译 / 批注：放在整段下方，与句子级区分样式 -->
              <div v-if="translationFor(p.hash, -1)" class="para-translation">
                <span class="para-line-tag">段译</span>
                <span class="para-line-content">{{ translationFor(p.hash, -1).translatedText }}</span>
                <button class="para-line-close" @click="removeTranslation(translationFor(p.hash, -1))">×</button>
              </div>

              <div v-for="n in notesFor(p.hash, -1)" :key="n.id" class="para-note">
                <span class="para-line-tag">段批</span>
                <span class="para-line-content">{{ n.content }}</span>
                <button class="para-line-close" @click="removeNote(n)">×</button>
              </div>

              <div class="para-actions">
                <button class="btn btn-ghost para-btn" :disabled="isBusy(`${p.hash}:-1`)" @click="translateParagraph(p)">
                  {{ translationFor(p.hash, -1) ? '重新生成段译' : '翻译整段' }}
                </button>
                <button class="btn btn-ghost para-btn" @click="openNote(p)">段落批注</button>
              </div>

              <!-- 段落批注同样就地展开 -->
              <InlineNoteEditor
                :open="paragraphNoteOpen(p.hash)"
                :saving="noteEditor.saving"
                placeholder="写这一整段的理解、结构或疑问…"
                @save="saveNote"
                @cancel="closeNote"
              />
            </div>
          </section>
        </article>
      </main>

      <!-- 右侧：标题目录 -->
      <aside class="right-pane" :class="{ open: tocOpen || mobileOpen === 'toc' }">
        <div class="pane-head">
          <strong>目录</strong>
          <button class="icon-btn" @click="toggleToc">×</button>
        </div>
        <nav class="toc-list">
          <button
            v-for="item in toc"
            :key="item.id"
            class="toc-item"
            :class="{ heading: item.heading, active: activeAnchor === item.id }"
            @click="scrollToAnchor(item.id)"
          >
            {{ item.label }}
          </button>
        </nav>
      </aside>

      <!-- 移动端遮罩 -->
      <Transition name="fade">
        <div v-if="mobileOpen" class="mobile-mask" @click="mobileOpen = ''" />
      </Transition>
    </div>

    <WordPopup
      :popup="popup"
      :existing="popupExisting"
      @choose="chooseSense"
      @close="closePopup"
      @remove="removeWordAnnotation"
    />

    <Transition name="toast">
      <div v-if="toast" class="toast">{{ toast }}</div>
    </Transition>
  </div>
</template>

<style scoped>
.reader-shell {
  height: 100vh;
  height: 100dvh;
  display: flex;
  flex-direction: column;
  background: var(--bg);
}

/* ---------- top bar ---------- */
.reader-topbar {
  height: 52px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 12px;
  background: #fff;
  border-bottom: 1px solid var(--line);
  z-index: 40;
}

.tb-left,
.tb-right {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.tb-title {
  display: flex;
  flex-direction: column;
  min-width: 0;
  margin-left: 4px;
}

.tb-title span {
  font-size: 14px;
  font-weight: 700;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 40vw;
}

.tb-title small {
  color: var(--muted);
  font-size: 11px;
}

.tb-back {
  font-size: 18px;
  color: #52525b;
  padding: 2px 6px;
}

.icon-btn {
  border: 1px solid var(--line);
  background: #fff;
  border-radius: 8px;
  width: 30px;
  height: 30px;
  font-size: 14px;
  color: #52525b;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.icon-btn:hover:not(:disabled) {
  border-color: #c7d2fe;
  color: var(--brand);
}

.icon-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.tb-user {
  font-size: 12.5px;
  color: var(--muted);
  max-width: 120px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tb-link,
.tb-login {
  border: 0;
  background: transparent;
  font-size: 13px;
  color: var(--brand);
  padding: 4px 8px;
}

/* ---------- layout ---------- */
.reader-body {
  flex: 1;
  min-height: 0;
  display: flex;
  position: relative;
}

.left-pane {
  width: 0;
  flex-shrink: 0;
  background: #fff;
  border-right: 1px solid var(--line);
  display: flex;
  flex-direction: column;
  transition: width 0.22s ease;
  overflow: hidden;
}

.left-pane.open {
  width: 264px;
}

.right-pane {
  width: 0;
  flex-shrink: 0;
  background: #fff;
  border-left: 1px solid var(--line);
  display: flex;
  flex-direction: column;
  transition: width 0.22s ease;
  overflow: hidden;
}

.right-pane.open {
  width: 250px;
}

.reading-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  scroll-behavior: smooth;
  padding: 34px clamp(20px, 5vw, 72px) 120px;
}

.pane-head {
  height: 48px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 14px;
  border-bottom: 1px solid var(--line);
}

.pane-head strong {
  font-size: 13px;
}

.pane-head small {
  display: block;
  color: var(--muted);
  font-size: 11px;
}

.article-list,
.toc-list {
  overflow-y: auto;
  padding: 8px;
  flex: 1;
}

.list-item {
  display: flex;
  gap: 8px;
  align-items: baseline;
  width: 100%;
  text-align: left;
  border: 0;
  background: transparent;
  border-radius: 9px;
  padding: 9px 10px;
  font-size: 13px;
  color: #3f3f46;
  line-height: 1.5;
}

.list-item:hover {
  background: #f4f4f5;
}

.list-item.active {
  background: var(--brand-soft);
  color: var(--brand);
}

.list-index {
  font-size: 11px;
  color: #a1a1aa;
  font-variant-numeric: tabular-nums;
}

.list-title {
  flex: 1;
}

/* ---------- article ---------- */
.article {
  max-width: 760px;
  margin: 0 auto;
}

.author,
.published {
  color: var(--muted);
}

.stale-notice {
  margin: 0 0 14px;
  padding: 9px 12px;
  border-radius: 9px;
  border-left: 3px solid #fcd34d;
  background: #fffbeb;
  color: #92400e;
  font-size: 13px;
  line-height: 1.6;
}

.article-head {
  margin-bottom: 30px;
  text-align: center;
}

.article-head h1 {
  font-family: var(--serif);
  font-size: clamp(26px, 4vw, 38px);
  line-height: 1.3;
  margin: 0 0 10px;
}

.subtitle {
  color: var(--muted);
  font-size: 14px;
  margin: 0 0 12px;
  font-style: italic;
}

.meta {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: #a1a1aa;
  font-size: 12.5px;
  margin: 0;
}

.para-heading {
  font-family: var(--serif);
  font-size: 24px;
  margin: 42px 0 18px;
  line-height: 1.4;
  scroll-margin-top: 16px;
  color: #18181b;
}

.paragraph-card {
  background: #fff;
  border: 1px solid var(--line);
  border-radius: 16px;
  padding: 18px 24px 14px;
  margin: 0 0 26px;
  box-shadow: var(--shadow-sm);
  scroll-margin-top: 16px;
  position: relative;
}

.paragraph-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 16px;
  bottom: 16px;
  width: 3px;
  border-radius: 3px;
  background: linear-gradient(#c7d2fe, #e9d5ff);
  opacity: 0.7;
}

.para-meta {
  font-size: 11.5px;
  color: #b8b8c0;
  margin-bottom: 2px;
  letter-spacing: 0.06em;
}

.para-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: 6px;
  padding-top: 8px;
  border-top: 1px dashed rgba(231, 229, 224, 0.8);
  opacity: 0;
  transition: opacity 0.15s;
}

.paragraph-card:hover .para-actions {
  opacity: 1;
}

.para-btn {
  padding: 5px 11px;
  font-size: 12.5px;
}

.para-translation {
  margin-top: 8px;
  display: flex;
  align-items: baseline;
  gap: 8px;
  background: #f0f9ff;
  border: 1px solid #bae6fd;
  border-radius: 10px;
  padding: 8px 12px;
  font-size: 14px;
  color: #075985;
  line-height: 1.8;
}

.para-note {
  margin-top: 8px;
  display: flex;
  align-items: baseline;
  gap: 8px;
  background: #f0fdf4;
  border: 1px solid #bbf7d0;
  border-radius: 10px;
  padding: 8px 12px;
  font-size: 14px;
  color: #166534;
  line-height: 1.8;
}

.para-line-tag {
  flex-shrink: 0;
  font-size: 11px;
  font-weight: 700;
  background: #e0f2fe;
  color: #0369a1;
  border-radius: 5px;
  padding: 1px 6px;
}

.para-note .para-line-tag {
  background: #dcfce7;
  color: #15803d;
}

.para-line-content {
  flex: 1;
  white-space: pre-wrap;
}

.para-line-close {
  border: 0;
  background: transparent;
  color: inherit;
  opacity: 0.6;
  font-size: 16px;
  flex-shrink: 0;
}

/* ---------- TOC ---------- */
.toc-item {
  display: block;
  width: 100%;
  text-align: left;
  border: 0;
  background: transparent;
  border-radius: 8px;
  padding: 7px 10px;
  font-size: 12px;
  color: #71717a;
  line-height: 1.5;
  border-left: 2px solid transparent;
}

.toc-item:hover {
  background: #f4f4f5;
}

.toc-item.heading {
  font-weight: 700;
  color: #3f3f46;
  margin-top: 4px;
}

.toc-item.active {
  color: var(--brand);
  background: var(--brand-soft);
  border-left-color: var(--brand);
}

/* ---------- overlays ---------- */
.mobile-mask {
  position: absolute;
  inset: 0;
  z-index: 30;
  background: rgba(24, 24, 27, 0.35);
}

.toast {
  position: fixed;
  left: 50%;
  bottom: 30px;
  transform: translateX(-50%);
  z-index: 130;
  background: #18181b;
  color: #fff;
  font-size: 13.5px;
  border-radius: 999px;
  padding: 10px 18px;
  box-shadow: var(--shadow-md);
  max-width: 80vw;
}

.toast-enter-active,
.toast-leave-active {
  transition: opacity 0.2s, transform 0.2s;
}

.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translate(-50%, 8px);
}

/* ---------- responsive ---------- */
@media (hover: none) {
  .para-actions {
    opacity: 1;
  }
}

/* 平板：左右栏改为抽屉，默认关闭 */
@media (max-width: 899px) {
  .left-pane.open,
  .right-pane.open {
    width: min(82vw, 300px);
  }

  .left-pane {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    z-index: 32;
    box-shadow: 12px 0 32px rgba(24, 24, 27, 0.12);
  }

  .right-pane {
    position: absolute;
    right: 0;
    top: 0;
    bottom: 0;
    z-index: 32;
    box-shadow: -12px 0 32px rgba(24, 24, 27, 0.12);
  }

  .toc-toggle {
    display: inline-flex;
  }

  .tb-user {
    display: none;
  }

  .reading-main {
    padding: 26px 24px 100px;
  }
}

/* 手机：收紧排版，放大触控目标 */
@media (max-width: 640px) {
  .reader-topbar {
    height: 48px;
    padding: 0 8px;
    gap: 4px;
  }

  .tb-left,
  .tb-right {
    gap: 6px;
  }

  .icon-btn {
    width: 34px;
    height: 34px;
    border-radius: 10px;
  }

  .tb-title {
    margin-left: 2px;
  }

  .tb-title span {
    font-size: 13px;
    max-width: 34vw;
  }

  .tb-title small {
    font-size: 10px;
  }

  .tb-back {
    font-size: 17px;
    padding: 4px 2px;
  }

  .reading-main {
    padding: 18px 14px 96px;
  }

  .article-head {
    margin-bottom: 20px;
  }

  .article-head h1 {
    font-size: 25px;
    line-height: 1.35;
  }

  .subtitle {
    font-size: 12.5px;
  }

  .meta {
    flex-wrap: wrap;
    gap: 6px 10px;
    font-size: 11.5px;
  }

  .para-heading {
    font-size: 21px;
    margin: 30px 0 14px;
  }

  .paragraph-card {
    padding: 13px 13px 10px;
    border-radius: 14px;
    margin-bottom: 18px;
  }

  .paragraph-card::before {
    top: 10px;
    bottom: 10px;
  }

  .para-meta {
    font-size: 10.5px;
  }

  .para-actions {
    gap: 6px;
  }

  .para-btn {
    padding: 7px 10px;
    font-size: 12px;
    min-height: 36px;
  }

  .para-translation,
  .para-note {
    font-size: 13.5px;
    padding: 7px 10px;
  }

  .left-pane.open,
  .right-pane.open {
    width: min(86vw, 300px);
  }

  .pane-head {
    height: 52px;
    padding-bottom: env(safe-area-inset-bottom, 0px);
  }

  .list-item,
  .toc-item {
    min-height: 44px;
    padding: 9px 12px;
  }

  .toast {
    bottom: 22px;
    font-size: 13px;
  }
}

@media (max-width: 360px) {
  .tb-title span {
    max-width: 28vw;
  }

  .tb-login {
    padding: 4px 2px;
  }

  .icon-btn {
    width: 31px;
    height: 31px;
  }

  .tb-right {
    gap: 4px;
  }
}

@media (min-width: 900px) {
  .toc-toggle {
    display: inline-flex;
  }
}
</style>
