import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { connectSettings, loadSettings, needsWelcome, saveSettings, signedOutSettings, validateBackendUrl } from './settings'

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

describe('demo mode and the welcome screen', () => {
  it('round-trips the demo flag', () => {
    saveSettings({ backendUrl: 'http://localhost:8080', apiKey: '', demo: true })
    expect(loadSettings().demo).toBe(true)
  })

  it('ignores a demo value that is not exactly true', () => {
    sessionStorage.setItem('attention.settings', JSON.stringify({ demo: 'yes' }))
    expect(loadSettings().demo).toBeUndefined()
  })

  it('asks on a first visit and not after a choice is saved', () => {
    expect(needsWelcome()).toBe(true)
    saveSettings({ backendUrl: 'http://localhost:8080', apiKey: '', demo: true })
    expect(needsWelcome()).toBe(false)
  })
})

describe('validateBackendUrl', () => {
  it('accepts http and https URLs', () => {
    expect(validateBackendUrl('http://localhost:8080')).toBeNull()
    expect(validateBackendUrl('https://svc.example.com/')).toBeNull()
  })

  it('rejects other schemes and non-URLs with advice', () => {
    expect(validateBackendUrl('ftp://host')).toBe('settings.urlScheme')
    expect(validateBackendUrl('localhost:8080')).not.toBeNull()
    expect(validateBackendUrl('')).toBe('settings.urlInvalid')
  })
})

describe('connecting and signing out', () => {
  it('"Connect" keeps the built-in service address and asks for no API key or demo, so sign-in comes next', () => {
    expect(connectSettings({ backendUrl: 'https://svc.example', apiKey: 'secret', demo: true })).toEqual({ backendUrl: 'https://svc.example', apiKey: '' })
  })

  it('signing out forgets an API key so a reload cannot walk straight back in', () => {
    expect(signedOutSettings({ backendUrl: 'https://svc.example', apiKey: 'secret' })).toEqual({ backendUrl: 'https://svc.example', apiKey: '' })
    expect(JSON.stringify(signedOutSettings({ backendUrl: 'https://svc.example', apiKey: 'secret' }))).not.toContain('secret')
  })
})
