import { afterEach, expect, it } from 'vitest'
import { ApiError, authorizationHeaders, renewToken, sessionVersion, setToken } from '../src/api/client'

afterEach(() => setToken(''))

it('renews a verified identity without treating existing streams as a different user', () => {
  setToken('first-access')
  const version = sessionVersion()
  renewToken('renewed-access')
  expect(sessionVersion()).toBe(version)
  expect(authorizationHeaders()).toEqual({ Authorization: 'Bearer renewed-access' })
})

it('cannot use renewal to establish an unauthenticated session', () => {
  setToken('')
  expect(() => renewToken('access')).toThrow(ApiError)
  expect(authorizationHeaders()).toEqual({})
})

it('rejects an empty renewal without destroying the current verified identity', () => {
  setToken('access')
  const version = sessionVersion()
  expect(() => renewToken('  ')).toThrow(ApiError)
  expect(sessionVersion()).toBe(version)
  expect(authorizationHeaders()).toEqual({ Authorization: 'Bearer access' })
})
