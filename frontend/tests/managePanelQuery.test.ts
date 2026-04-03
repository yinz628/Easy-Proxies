import assert from 'node:assert/strict'
import test from 'node:test'

import {
  canSelectFilteredResults,
  defaultManageQuery,
  hasActiveManageFilters,
  hasPendingManageKeywordChange,
  resolveVisibleManageQuery,
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
