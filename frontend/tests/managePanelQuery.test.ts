import assert from 'node:assert/strict'
import test from 'node:test'

import {
  canSelectFilteredResults,
  defaultManageQuery,
  getStoredManagePageSize,
  hasActiveManageFilters,
  hasPendingManageKeywordChange,
  normalizeManagePageSize,
  resolveVisibleManageQuery,
  saveManagePageSize,
} from '../src/components/managePanelQuery.ts'

test('resolveVisibleManageQuery uses latest keyword input before debounce sync', () => {
  const visible = resolveVisibleManageQuery({
    ...defaultManageQuery,
    keyword: '',
    status: 'normal',
  }, ' hk ')

  assert.equal(visible.keyword, 'hk')
  assert.equal(visible.status, 'normal')
})

test('hasPendingManageKeywordChange detects unsynced keyword input', () => {
  assert.equal(hasPendingManageKeywordChange(defaultManageQuery, ''), false)
  assert.equal(hasPendingManageKeywordChange(defaultManageQuery, 'us'), true)
  assert.equal(hasPendingManageKeywordChange({
    ...defaultManageQuery,
    keyword: 'jp',
  }, 'jp'), false)
})

test('hasActiveManageFilters treats pending keyword input as an active filter', () => {
  assert.equal(hasActiveManageFilters(defaultManageQuery, ''), false)
  assert.equal(hasActiveManageFilters(defaultManageQuery, 'sg'), true)
  assert.equal(hasActiveManageFilters({
    ...defaultManageQuery,
    lifecycle_state: 'staged',
  }, ''), true)
})

test('canSelectFilteredResults blocks full selection when filter is empty or pending', () => {
  assert.equal(canSelectFilteredResults(defaultManageQuery, '', 68), false)
  assert.equal(canSelectFilteredResults(defaultManageQuery, 'hk', 68), false)
  assert.equal(canSelectFilteredResults({
    ...defaultManageQuery,
    keyword: 'hk',
  }, 'hk', 12), true)
})

test('normalizeManagePageSize only accepts supported values', () => {
  assert.equal(normalizeManagePageSize(50), 50)
  assert.equal(normalizeManagePageSize(200), 200)
  assert.equal(normalizeManagePageSize(999), defaultManageQuery.page_size)
  assert.equal(normalizeManagePageSize(undefined), defaultManageQuery.page_size)
})

test('manage page size preference reads and writes supported values', () => {
  const storage = new Map<string, string>()
  const mockStorage = {
    getItem(key: string) {
      return storage.has(key) ? storage.get(key)! : null
    },
    setItem(key: string, value: string) {
      storage.set(key, value)
    },
  }

  assert.equal(getStoredManagePageSize(mockStorage), defaultManageQuery.page_size)

  saveManagePageSize(200, mockStorage)
  assert.equal(getStoredManagePageSize(mockStorage), 200)

  storage.set('easy-proxies.manage.page-size', '13')
  assert.equal(getStoredManagePageSize(mockStorage), defaultManageQuery.page_size)
})
