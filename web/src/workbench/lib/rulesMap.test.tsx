import { expect, test } from 'vitest'
import type { InterceptRule } from '@/types'
import { toInterceptRule, toLocalRule } from './rulesMap'

function fromFlowMock(): InterceptRule {
  return {
    id: 'rule-1',
    name: 'mock',
    enabled: true,
    priority: 10,
    logicOperator: 'AND',
    conditions: [],
    createdAt: '',
    updatedAt: '',
    actions: [
      {
        type: 'auto_respond',
        enabled: true,
        parameters: {
          response: {
            status: 200,
            body: '{"a":1}',
            contentType: '',
            headers: { 'Set-Cookie': ['a=1', 'b=2'] },
          },
          final: true,
        },
      },
    ],
  }
}

test('未改动的回放 mock 保留 final 与原参数', () => {
  const params = toInterceptRule(toLocalRule(fromFlowMock())).actions[0]
    .parameters
  expect(params.final).toBe(true)
  expect(params.response).toEqual(fromFlowMock().actions[0].parameters.response)
})

test.each([
  ['状态码', { param: '404' }],
  ['正文', { extra: '{"a":2}' }],
])('改过%s的回放 mock 去掉 final，其余参数保留', (_, patch) => {
  const local = toLocalRule(fromFlowMock())
  local.actions[0] = { ...local.actions[0], ...patch }
  const params = toInterceptRule(local).actions[0].parameters
  expect(params.final).toBeUndefined()
  expect(params.response?.contentType).toBe('')
  expect(params.response?.headers).toEqual({ 'Set-Cookie': ['a=1', 'b=2'] })
})
