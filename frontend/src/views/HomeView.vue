<script setup>
import { onMounted, ref } from 'vue'
import { contentApi } from '../api'

const datasets = ref([])
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    const data = await contentApi.datasets()
    datasets.value = data.datasets
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <h1 class="page-title">英文阅读批注</h1>
    <p class="page-subtitle">
      点词查义 · 标注词性词义 · 句段翻译 · 随手批注。先从一本书开始精读。
    </p>

    <div v-if="loading" class="center-wrap"><div class="spinner" /></div>
    <p v-else-if="error" class="error-text">{{ error }}</p>

    <div v-else class="card-grid">
      <RouterLink
        v-for="d in datasets"
        :key="d.id"
        class="dataset-card"
        :to="`/dataset/${d.id}`"
      >
        <div class="emoji" :style="{ background: d.color }">{{ d.emoji }}</div>
        <h3>{{ d.title }}</h3>
        <p>{{ d.description }}</p>
        <div class="meta">{{ d.articleCount }} 篇文章 · 进入阅读 →</div>
      </RouterLink>
    </div>
  </div>
</template>
