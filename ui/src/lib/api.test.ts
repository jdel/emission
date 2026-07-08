import { describe, expect, it } from 'vitest'

import { isAuthEnabled, proxyFormatError } from '@/lib/api'

describe('isAuthEnabled', () => {
  it.each([
    [{ authenticated: false }, true], // not logged in -> auth is configured
    [{ authenticated: true, username: 'alice' }, true], // logged in -> auth is configured
    [{ authenticated: true }, false], // auth-off server's implicit "always authenticated, no user"
    [{ authenticated: true, username: '' }, false], // empty username treated same as missing
  ])('isAuthEnabled(%p) -> %p', (status, want) => {
    expect(isAuthEnabled(status)).toBe(want)
  })
})

describe('proxyFormatError', () => {
  it.each([
    ['', null], // empty = direct, valid
    ['http://example.com:8080', null],
    ['socks5://example.com:1080', null],
    ['ftp://example.com:21', 'expect error'],
    ['http://example.com', 'expect error'], // missing port
    ['http://example.com:99999', 'expect error'], // port out of range
    ['http://localhost:8080', 'expect error'],
    ['http://127.0.0.1:8080', 'expect error'],
    ['http://192.168.1.1:8080', 'expect error'],
    ['http://10.0.0.1:8080', 'expect error'],
    ['http://169.254.1.1:8080', 'expect error'],
  ])('proxyFormatError(%p)', (input, expectation) => {
    if (expectation === null) expect(proxyFormatError(input)).toBeNull()
    else expect(proxyFormatError(input)).toEqual(expect.any(String))
  })
})
