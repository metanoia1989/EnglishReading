<script setup>
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { contentApi } from '../api'

const route = useRoute()
const dataset = ref(null)
const articles = ref([])
const loading = ref(true)
const error = ref('')
const keyword = ref('')

const filtered = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) return articles.value
  return articles.value.filter(
    (a) => a.title.toLowerCase().includes(q) || (a.subtitle || '').toLowerCase().includes(q),
  )
})

onMounted(async () => {
  try {
    const data = await contentApi.datasetArticles(route.params.id)
    dataset.value = data.dataset
    articles.value = data.articles
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <div v-if="loading" class="center-wrap"><div class="spinner" /></div>
    <p v-else-if="error" class="error-text">{{ error }}</p>

    <template v-else>
      <RouterLink to="/" class="back-link">← 返回书库</RouterLink>
      <h1 class="page-title" style="margin-top: 14px">
        {{ dataset.emoji }} {{ dataset.title }}
      </h1>
      <p class="page-subtitle">{{ dataset.description }}</p>

      <input v-model="keyword" class="input search" placeholder="搜索文章标题…" />

      <p v-if="!filtered.length" class="page-subtitle">没有匹配的文章</p>
      <RouterLink
        v-for="a in filtered"
        :key="a.id"
        class="article-card"
        :to="`/read/${a.id}`"
      >
        <div>
          <h3>{{ a.title }}</h3>
          <p class="sub">{{ a.subtitle }} · {{ a.paragraphCount }} 段</p>
        </div>
        <span class="level-tag">{{ a.level }}</span>
      </RouterLink>
    </template>
  </div>
</template>

<style scoped>
.back-link {
  color: var(--muted);
  font-size: 14px;
}

.back-link:hover {
  color: var(--brand);
}

.search {
  margin-bottom: 22px;
  max-width: 420px;
}

@media (max-width: 640px) {
  .search {
    max-width: none;
    font-size: 16px;
  }

  .article-card {
    padding: 14px;
    flex-direction: column;
    align-items: flex-start;
    gap: 10px;
  }

  .article-card h3 {
    font-size: 15.5px;
    line-height: 1.45;
  }

  .article-card .sub {
    font-size: 12.5px;
  }

  .level-tag {
    font-size: 11px;
  }
}
</style>
