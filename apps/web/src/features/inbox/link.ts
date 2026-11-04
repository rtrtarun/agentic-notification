/** A link that opens the inbox with this notification's drawer already open. */
export function notificationLink(id: string, location: Pick<Location, 'origin' | 'pathname' | 'search'> = window.location): string {
  return `${location.origin}${location.pathname}${location.search}#/inbox?open=${encodeURIComponent(id)}`
}

/** Copies the link to the clipboard. Returns false when the browser refuses. */
export async function copyNotificationLink(id: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(notificationLink(id))
    return true
  } catch {
    return false
  }
}
