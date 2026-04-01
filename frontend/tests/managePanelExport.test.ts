import assert from 'node:assert/strict'
import test from 'node:test'

import { buildSelectedExportText } from '../src/components/managePanelExport.ts'

test('buildSelectedExportText exports selected node URIs in current order', () => {
  const text = buildSelectedExportText(
    [
      { name: 'node-a', uri: 'trojan://a' },
      { name: 'node-b', uri: 'trojan://b' },
      { name: 'node-c', uri: 'trojan://c' },
    ],
    new Set(['node-c', 'node-a']),
  )

  assert.equal(text, 'trojan://a\ntrojan://c')
})

test('buildSelectedExportText returns empty text when nothing is selected', () => {
  const text = buildSelectedExportText(
    [
      { name: 'node-a', uri: 'trojan://a' },
    ],
    new Set(),
  )

  assert.equal(text, '')
})
