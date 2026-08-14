import { auth } from '../store/auth'

const BASE = '/api'

export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.status = status
  }
}

export async function api(path, { method = 'GET', body, authRequired = false } = {}) {
  const headers = { 'Content-Type': 'application/json' }
  if (auth.token) headers.Authorization = `Bearer ${auth.token}`
  const res = await fetch(BASE + path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })

  let payload = null
  const text = await res.text()
  if (text) {
    try {
      payload = JSON.parse(text)
    } catch {
      payload = text
    }
  }

  if (!res.ok) {
    const message = payload?.error || `请求失败 (${res.status})`
    if (res.status === 401 && authRequired) {
      auth.clear()
    }
    throw new ApiError(message, res.status)
  }
  return payload
}

export const authApi = {
  register: (data) => api('/auth/register', { method: 'POST', body: data }),
  verify: (data) => api('/auth/verify', { method: 'POST', body: data }),
  login: (data) => api('/auth/login', { method: 'POST', body: data }),
  me: () => api('/auth/me', { authRequired: true }),
  logout: () => api('/auth/logout', { method: 'POST', authRequired: true }),
}

export const contentApi = {
  datasets: () => api('/datasets'),
  datasetArticles: (id) => api(`/datasets/${id}/articles`),
  article: (id) => api(`/articles/${id}`),
  state: (id) => api(`/articles/${id}/state`, { authRequired: true }),
  lookup: (word) => api(`/dict/lookup?word=${encodeURIComponent(word)}`),
  saveWordAnnotation: (articleId, data) =>
    api(`/articles/${articleId}/word-annotations`, {
      method: 'POST',
      body: data,
      authRequired: true,
    }),
  deleteWordAnnotation: (articleId, annotationId) =>
    api(`/articles/${articleId}/word-annotations/${annotationId}`, {
      method: 'DELETE',
      authRequired: true,
    }),
  createNote: (articleId, data) =>
    api(`/articles/${articleId}/notes`, { method: 'POST', body: data, authRequired: true }),
  deleteNote: (articleId, noteId) =>
    api(`/articles/${articleId}/notes/${noteId}`, { method: 'DELETE', authRequired: true }),
  translate: (articleId, data) =>
    api(`/articles/${articleId}/translations`, {
      method: 'POST',
      body: data,
      authRequired: true,
    }),
  deleteTranslation: (articleId, translationId) =>
    api(`/articles/${articleId}/translations/${translationId}`, {
      method: 'DELETE',
      authRequired: true,
    }),
}
