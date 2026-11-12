import test from 'node:test'
import assert from 'node:assert/strict'
import { cleanHost, fromBizStatusRules, hasBizRules, toBizStatusRules } from './bizStatus.ts'

test('只下发填好主机与字段的规则', () => {
  const drafts = [
    { host: ' *.example.com ', fields: 'code, errcode，ret', success: '0, 200', messageField: ' msg ' },
    { host: 'half.typed', fields: ' ', success: '', messageField: '' },
    { host: '', fields: 'code', success: '', messageField: '' },
    { host: '10.0.3.21:8080', fields: 'status', success: '', messageField: '' },
  ]
  assert.deepEqual(toBizStatusRules(drafts), [
    { host: '*.example.com', fields: ['code', 'errcode', 'ret'], success: ['0', '200'], messageField: 'msg' },
    { host: '10.0.3.21:8080', fields: ['status'] },
  ])
  assert.equal(hasBizRules(drafts), true)
  assert.equal(hasBizRules(drafts.slice(1, 3)), false)
})

test('后端配置还原为编辑态', () => {
  const rules = [{ host: 'a.com', fields: ['code', 'ret'], success: ['0'] }]
  assert.deepEqual(fromBizStatusRules(rules), [{ host: 'a.com', fields: 'code, ret', success: '0', messageField: '' }])
  assert.deepEqual(toBizStatusRules(fromBizStatusRules(rules)), rules)
})

test('主机去掉协议前缀与路径，保留端口', () => {
  assert.equal(cleanHost(' HTTPS://Api.Example.com/v1/users?id=1 '), 'api.example.com')
  assert.equal(cleanHost('http://10.0.3.21:8080'), '10.0.3.21:8080')
  assert.equal(cleanHost('https://*.example.com/'), '*.example.com')
  assert.equal(cleanHost('api.example.com'), 'api.example.com')
  assert.deepEqual(toBizStatusRules([{ host: 'https://', fields: 'code', success: '', messageField: '' }]), [])
})
