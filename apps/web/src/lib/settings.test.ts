import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loadSettings, saveSettings, validateBackendUrl } from './settings'

function fakeSessionStorage() {
  const data = new Map<string, string>()
  return {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
  }
}

beforeEach(() => {
  vi.stubGlobal('sessionStorage', fakeSessionStorage())
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('loadSettings and saveSettings', () => {
  it('falls back to the local service with no key', () => {
    expect(loadSettings()).toEqual({ backendUrl: 'http://localhost:8080', apiKey: '' })
  })

  it('round-trips saved settings', () => {
    saveSettings({ backendUrl: 'https://svc.example.com', apiKey: 'secret' })
    expect(loadSettings()).toEqual({ backendUrl: 'https://svc.example.com', apiKey: 'secret' })
  })

  it('ignores corrupt storage', () => {
    sessionStorage.setItem('attention.settings', '{not json')
    expect(loadSettings().backendUrl).toBe('http://localhost:8080')
  })

  it('ignores values of the wrong type', () => {
    sessionStorage.setItem('attention.settings', JSON.stringify({ backendUrl: 5, apiKey: {} }))
    expect(loadSettings()).toEqual({ backendUrl: 'http://localhost:8080', apiKey: '' })
  })
})

describe('validateBackendUrl', () => {
  it('accepts http and https URLs', () => {
    expect(validateBackendUrl('http://localhost:8080')).toBeNull()
    expect(validateBackendUrl('https://svc.example.com/')).toBeNull()
  })

  it('rejects other schemes and non-URLs with advice', () => {
    expect(validateBackendUrl('ftp://host')).toMatch(/http/)
    expect(validateBackendUrl('localhost:8080')).not.toBeNull()
    expect(validateBackendUrl('')).toMatch(/full URL/)
  })
})
