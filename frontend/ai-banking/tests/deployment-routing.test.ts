import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('deployed product routing', () => {
  it('keeps the Go authentication and route whitelist in the browser task path', () => {
    const nginx = readFileSync('../../deploy/banking-web/nginx.conf', 'utf8')
    expect(nginx).toContain('location ^~ /product/api/v1/ { proxy_pass http://$banking_upstream$request_uri; }')
  })
})
